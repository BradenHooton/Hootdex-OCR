// Package fsnotify is the inbound hot-folder adapter for RICOH fi-8000 duplex output.
// It debounces files until their size is stable, pairs front/back scans, archives the
// originals under <archive>/<batch_id>/ and enqueues them through ports.ScanIntake.
package fsnotify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	fsn "github.com/fsnotify/fsnotify"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// Config tunes the watcher. Zero durations take the defaults.
type Config struct {
	InboxDir      string
	ArchiveDir    string
	StableFor     time.Duration // file size must be unchanged this long (default 250ms)
	PollInterval  time.Duration // size polling cadence (default 50ms)
	PairWindow    time.Duration // un-suffixed files arriving within this window pair up (default 1500ms)
	OrphanTimeout time.Duration // a named side waits this long for its partner (default 10s)
	RetryDelay    time.Duration // re-attempt after a failed enqueue (default 30s)
	Logger        *slog.Logger
}

func (c *Config) defaults() {
	if c.StableFor <= 0 {
		c.StableFor = 250 * time.Millisecond
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 50 * time.Millisecond
	}
	if c.PairWindow <= 0 {
		c.PairWindow = 1500 * time.Millisecond
	}
	if c.OrphanTimeout <= 0 {
		c.OrphanTimeout = 10 * time.Second
	}
	if c.RetryDelay <= 0 {
		c.RetryDelay = 30 * time.Second
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

var (
	// <prefix>_front.jpg / <prefix>_back.jpg / <prefix>_001.jpg / <prefix>_002.jpg
	namedRe  = regexp.MustCompile(`(?i)^(.+)_(front|back|001|002)\.(jpe?g|png|tiff?)$`)
	imageExt = regexp.MustCompile(`(?i)\.(jpe?g|png|tiff?)$`)
)

type partial struct {
	front, back string
	timer       *time.Timer
}

type loose struct {
	path  string
	timer *time.Timer
}

// Watcher monitors the inbox directory.
type Watcher struct {
	cfg    Config
	intake ports.ScanIntake

	mu       sync.Mutex
	tracking map[string]bool      // paths currently being debounced
	cooldown map[string]time.Time // paths whose enqueue failed recently
	pending  map[string]*partial  // named prefix -> partial pair
	loose    *loose
	closed   bool
	wg       sync.WaitGroup
}

// New validates the config and builds a watcher.
func New(cfg Config, intake ports.ScanIntake) (*Watcher, error) {
	cfg.defaults()
	if cfg.InboxDir == "" || cfg.ArchiveDir == "" {
		return nil, fmt.Errorf("fsnotify.New: inbox and archive directories are required")
	}
	if intake == nil {
		return nil, fmt.Errorf("fsnotify.New: intake is required")
	}
	return &Watcher{cfg: cfg, intake: intake, tracking: map[string]bool{}, cooldown: map[string]time.Time{}, pending: map[string]*partial{}}, nil
}

// Run watches the inbox until ctx is cancelled. Files already present at startup are ingested too.
func (w *Watcher) Run(ctx context.Context) error {
	for _, d := range []string{w.cfg.InboxDir, w.cfg.ArchiveDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("fsnotify.Run: mkdir %s: %w", d, err)
		}
	}
	fw, err := fsn.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify.Run: %w", err)
	}
	defer fw.Close()
	if err := fw.Add(w.cfg.InboxDir); err != nil {
		return fmt.Errorf("fsnotify.Run: watch %s: %w", w.cfg.InboxDir, err)
	}
	entries, err := os.ReadDir(w.cfg.InboxDir)
	if err != nil {
		return fmt.Errorf("fsnotify.Run: scan %s: %w", w.cfg.InboxDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			w.track(ctx, filepath.Join(w.cfg.InboxDir, e.Name()))
		}
	}
	w.cfg.Logger.InfoContext(ctx, "hot-folder watcher started", "inbox", w.cfg.InboxDir, "archive", w.cfg.ArchiveDir)
	defer w.shutdown()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-fw.Events:
			if !ok {
				return nil
			}
			if ev.Has(fsn.Create) || ev.Has(fsn.Write) {
				w.track(ctx, ev.Name)
			}
		case err, ok := <-fw.Errors:
			if !ok {
				return nil
			}
			w.cfg.Logger.ErrorContext(ctx, "watcher error", "err", err)
		}
	}
}

func (w *Watcher) shutdown() {
	w.mu.Lock()
	w.closed = true
	for _, p := range w.pending {
		p.timer.Stop()
	}
	if w.loose != nil {
		w.loose.timer.Stop()
	}
	w.mu.Unlock()
	w.wg.Wait()
}

// track starts a debounce goroutine for an image path (once per path).
func (w *Watcher) track(ctx context.Context, path string) {
	if !imageExt.MatchString(path) || strings.HasPrefix(filepath.Base(path), ".") {
		return
	}
	w.mu.Lock()
	if w.closed || w.tracking[path] {
		w.mu.Unlock()
		return
	}
	if until, ok := w.cooldown[path]; ok && time.Now().Before(until) {
		w.mu.Unlock()
		return
	}
	w.tracking[path] = true
	w.wg.Add(1)
	w.mu.Unlock()
	go func() {
		defer w.wg.Done()
		stable := w.waitStable(ctx, path)
		w.mu.Lock()
		delete(w.tracking, path)
		w.mu.Unlock()
		if stable {
			w.ready(ctx, path)
		}
	}()
}

// waitStable polls until the file size has not changed for StableFor and the file can be opened.
func (w *Watcher) waitStable(ctx context.Context, path string) bool {
	var lastSize int64 = -1
	lastChange := time.Now()
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()
	for {
		fi, err := os.Stat(path)
		if err != nil || fi.IsDir() {
			return false // moved away or deleted
		}
		if fi.Size() != lastSize {
			lastSize, lastChange = fi.Size(), time.Now()
		} else if fi.Size() > 0 && time.Since(lastChange) >= w.cfg.StableFor {
			if f, err := os.Open(path); err == nil { // lock released by the scanner driver
				_ = f.Close()
				return true
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// ready routes a stable file into named pairing or the timestamp-window pairing.
func (w *Watcher) ready(ctx context.Context, path string) {
	base := filepath.Base(path)
	if m := namedRe.FindStringSubmatch(base); m != nil {
		prefix, side := m[1], strings.ToLower(m[2])
		w.mu.Lock()
		p := w.pending[prefix]
		if p == nil {
			p = &partial{}
			w.pending[prefix] = p
			p.timer = w.afterFunc(w.cfg.OrphanTimeout, func() { w.flushPartial(ctx, prefix) })
		}
		if side == "front" || side == "001" {
			p.front = path
		} else {
			p.back = path
		}
		complete := p.front != "" && p.back != ""
		if complete {
			p.timer.Stop()
			delete(w.pending, prefix)
		}
		w.mu.Unlock()
		if complete {
			w.emit(ctx, domain.ScanPair{BatchID: batchFromPrefix(prefix), Prefix: prefix, FrontPath: p.front, BackPath: p.back})
		}
		return
	}

	// Un-suffixed scanner output: pair consecutive files arriving within PairWindow.
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	w.mu.Lock()
	if l := w.loose; l != nil && l.path != path {
		l.timer.Stop()
		w.loose = nil
		w.mu.Unlock()
		lstem := strings.TrimSuffix(filepath.Base(l.path), filepath.Ext(l.path))
		w.emit(ctx, domain.ScanPair{BatchID: batchFromPrefix(lstem), Prefix: lstem, FrontPath: l.path, BackPath: path})
		return
	}
	l := &loose{path: path}
	l.timer = w.afterFunc(w.cfg.PairWindow, func() {
		w.mu.Lock()
		if w.loose != l {
			w.mu.Unlock()
			return
		}
		w.loose = nil
		w.mu.Unlock()
		w.emit(ctx, domain.ScanPair{BatchID: batchFromPrefix(stem), Prefix: stem, FrontPath: path})
	})
	w.loose = l
	w.mu.Unlock()
}

// afterFunc is time.AfterFunc tracked by the wait group so shutdown waits for in-flight emits.
func (w *Watcher) afterFunc(d time.Duration, fn func()) *time.Timer {
	return time.AfterFunc(d, func() {
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			return
		}
		w.wg.Add(1)
		w.mu.Unlock()
		defer w.wg.Done()
		fn()
	})
}

// flushPartial emits a named side whose partner never arrived (single-sided scan).
func (w *Watcher) flushPartial(ctx context.Context, prefix string) {
	w.mu.Lock()
	p := w.pending[prefix]
	delete(w.pending, prefix)
	w.mu.Unlock()
	if p == nil {
		return
	}
	front, back := p.front, p.back
	if front == "" {
		front, back = back, ""
	}
	w.cfg.Logger.WarnContext(ctx, "scan partner never arrived; ingesting single side", "prefix", prefix, "path", front)
	w.emit(ctx, domain.ScanPair{BatchID: batchFromPrefix(prefix), Prefix: prefix, FrontPath: front, BackPath: back})
}

// emit archives the originals and enqueues the job. If enqueueing fails the files are
// moved back to the inbox and retried after RetryDelay, so no scan is ever dropped.
func (w *Watcher) emit(ctx context.Context, pair domain.ScanPair) {
	if ctx.Err() != nil {
		return
	}
	dir := filepath.Join(w.cfg.ArchiveDir, pair.BatchID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		w.cfg.Logger.ErrorContext(ctx, "archive mkdir failed", "dir", dir, "err", err)
		return
	}
	archived := pair
	moved := map[string]string{} // archive path -> original
	for _, src := range []*string{&archived.FrontPath, &archived.BackPath} {
		if *src == "" {
			continue
		}
		dst, err := moveFile(*src, dir)
		if err != nil {
			w.cfg.Logger.ErrorContext(ctx, "archive move failed", "path", *src, "err", err)
			w.restore(ctx, moved)
			return
		}
		moved[dst] = *src
		*src = dst
	}
	if _, err := w.intake.EnqueueScan(ctx, archived); err != nil {
		w.cfg.Logger.ErrorContext(ctx, "enqueue failed; returning scan to inbox", "prefix", pair.Prefix, "err", err)
		w.restore(ctx, moved)
	}
}

func (w *Watcher) restore(ctx context.Context, moved map[string]string) {
	for dst, orig := range moved {
		w.mu.Lock()
		w.cooldown[orig] = time.Now().Add(w.cfg.RetryDelay)
		w.mu.Unlock()
		if err := renameOrCopy(dst, orig); err != nil {
			w.cfg.Logger.ErrorContext(ctx, "restore to inbox failed", "path", dst, "err", err)
			continue
		}
		orig := orig
		w.afterFunc(w.cfg.RetryDelay, func() {
			w.mu.Lock()
			delete(w.cooldown, orig)
			w.mu.Unlock()
			w.track(ctx, orig)
		})
	}
}

// batchFromPrefix derives the batch from "<batch>_<seq>": "B1_001" -> "B1"; "SCAN" -> "SCAN".
func batchFromPrefix(prefix string) string {
	if i := strings.LastIndex(prefix, "_"); i > 0 {
		return sanitizeBatch(prefix[:i])
	}
	return sanitizeBatch(prefix)
}

func sanitizeBatch(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == '.' {
			return '-'
		}
		return r
	}, s)
	if s == "" {
		return "UNBATCHED"
	}
	return s
}

// moveFile moves src into dir, avoiding name collisions, and returns the new path.
func moveFile(src, dir string) (string, error) {
	base := filepath.Base(src)
	dst := filepath.Join(dir, base)
	for i := 1; ; i++ {
		if _, err := os.Stat(dst); errors.Is(err, os.ErrNotExist) {
			break
		}
		ext := filepath.Ext(base)
		dst = filepath.Join(dir, fmt.Sprintf("%s.%d%s", strings.TrimSuffix(base, ext), i, ext))
	}
	if err := renameOrCopy(src, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// renameOrCopy renames, falling back to copy+remove across filesystems.
func renameOrCopy(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("move %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("move %s: %w", src, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("move %s: %w", src, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("move %s: %w", src, err)
	}
	_ = in.Close()
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("move %s: remove: %w", src, err)
	}
	return nil
}
