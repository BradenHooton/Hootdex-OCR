package fsnotify

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hootyeah/hootdex/internal/domain"
)

type fakeIntake struct {
	mu    sync.Mutex
	pairs []domain.ScanPair
	fail  int // fail the first N calls
	calls int
}

func (f *fakeIntake) EnqueueScan(_ context.Context, p domain.ScanPair) (*domain.IngestionJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.fail {
		return nil, errors.New("db down")
	}
	f.pairs = append(f.pairs, p)
	return &domain.IngestionJob{ID: "j", BatchID: p.BatchID}, nil
}

func (f *fakeIntake) snapshot() []domain.ScanPair {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.ScanPair(nil), f.pairs...)
}

func startWatcher(t *testing.T, cfg Config, intake *fakeIntake) (Config, func()) {
	t.Helper()
	root := t.TempDir()
	cfg.InboxDir = filepath.Join(root, "inbox")
	cfg.ArchiveDir = filepath.Join(root, "archive")
	if err := os.MkdirAll(cfg.InboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := New(cfg, intake)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	time.Sleep(50 * time.Millisecond) // let the fsnotify watch register
	return cfg, func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("run: %v", err)
		}
	}
}

func waitPairs(t *testing.T, f *fakeIntake, n int, timeout time.Duration) []domain.ScanPair {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p := f.snapshot(); len(p) >= n {
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d pairs, got %d", n, len(f.snapshot()))
	return nil
}

// writeSlowly simulates the scanner driver streaming a file in chunks.
func writeSlowly(path string, data []byte, chunks int, gap time.Duration) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	step := len(data) / chunks
	for i := 0; i < chunks; i++ {
		end := (i + 1) * step
		if i == chunks-1 {
			end = len(data)
		}
		if _, err := f.Write(data[i*step : end]); err != nil {
			return err
		}
		_ = f.Sync()
		time.Sleep(gap)
	}
	return f.Close()
}

func TestWatcherPairsNamedDuplexAndArchives(t *testing.T) {
	intake := &fakeIntake{}
	cfg, stop := startWatcher(t, Config{}, intake)
	defer stop()

	payload := bytes.Repeat([]byte("JPEGDATA"), 4096)
	// The front streams in for ~400ms with 100ms gaps: it must not be picked up mid-write.
	go func() {
		if err := writeSlowly(filepath.Join(cfg.InboxDir, "B7_0001_front.jpg"), payload, 4, 100*time.Millisecond); err != nil {
			t.Error(err)
		}
	}()
	if err := os.WriteFile(filepath.Join(cfg.InboxDir, "B7_0001_back.jpg"), payload, 0o644); err != nil {
		t.Fatal(err)
	}

	pairs := waitPairs(t, intake, 1, 5*time.Second)
	p := pairs[0]
	if p.BatchID != "B7" || p.Prefix != "B7_0001" {
		t.Fatalf("pair ids: %+v", p)
	}
	if filepath.Dir(p.FrontPath) != filepath.Join(cfg.ArchiveDir, "B7") || filepath.Base(p.BackPath) != "B7_0001_back.jpg" {
		t.Fatalf("not archived: %+v", p)
	}
	got, err := os.ReadFile(p.FrontPath)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("front archived before write finished (%d of %d bytes)", len(got), len(payload))
	}
	if _, err := os.Stat(filepath.Join(cfg.InboxDir, "B7_0001_front.jpg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("original still in inbox")
	}
}

func TestWatcherPairsNumericSuffixAndIgnoresNonImages(t *testing.T) {
	intake := &fakeIntake{}
	cfg, stop := startWatcher(t, Config{StableFor: 100 * time.Millisecond}, intake)
	defer stop()
	for _, n := range []string{"BATCH9_002.jpg", "BATCH9_001.jpg", "notes.txt", ".hidden.jpg"} {
		if err := os.WriteFile(filepath.Join(cfg.InboxDir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := waitPairs(t, intake, 1, 3*time.Second)[0]
	if filepath.Base(p.FrontPath) != "BATCH9_001.jpg" || filepath.Base(p.BackPath) != "BATCH9_002.jpg" || p.BatchID != "BATCH9" {
		t.Fatalf("pair: %+v", p)
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(intake.snapshot()); n != 1 {
		t.Fatalf("non-image files were ingested: %d pairs", n)
	}
}

func TestWatcherTimestampWindowAndOrphans(t *testing.T) {
	intake := &fakeIntake{}
	cfg, stop := startWatcher(t, Config{StableFor: 60 * time.Millisecond, PairWindow: 600 * time.Millisecond, OrphanTimeout: 400 * time.Millisecond}, intake)
	defer stop()

	// Two un-suffixed files within the window pair up.
	_ = os.WriteFile(filepath.Join(cfg.InboxDir, "scan0001.jpg"), []byte("a"), 0o644)
	time.Sleep(150 * time.Millisecond)
	_ = os.WriteFile(filepath.Join(cfg.InboxDir, "scan0002.jpg"), []byte("b"), 0o644)
	p := waitPairs(t, intake, 1, 3*time.Second)[0]
	if filepath.Base(p.FrontPath) != "scan0001.jpg" || filepath.Base(p.BackPath) != "scan0002.jpg" {
		t.Fatalf("window pair: %+v", p)
	}
	// A named front whose back never arrives is ingested single-sided after OrphanTimeout.
	_ = os.WriteFile(filepath.Join(cfg.InboxDir, "B2_0005_front.jpg"), []byte("c"), 0o644)
	pairs := waitPairs(t, intake, 2, 3*time.Second)
	if pairs[1].BackPath != "" || filepath.Base(pairs[1].FrontPath) != "B2_0005_front.jpg" {
		t.Fatalf("orphan: %+v", pairs[1])
	}
}

func TestWatcherIngestsExistingFilesAndRetriesFailedEnqueue(t *testing.T) {
	intake := &fakeIntake{fail: 1}
	root := t.TempDir()
	cfg := Config{InboxDir: filepath.Join(root, "inbox"), ArchiveDir: filepath.Join(root, "archive"),
		StableFor: 50 * time.Millisecond, RetryDelay: 300 * time.Millisecond}
	_ = os.MkdirAll(cfg.InboxDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfg.InboxDir, "PRE_0001_front.jpg"), []byte("f"), 0o644)
	_ = os.WriteFile(filepath.Join(cfg.InboxDir, "PRE_0001_back.jpg"), []byte("b"), 0o644)
	w, err := New(cfg, intake)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	p := waitPairs(t, intake, 1, 4*time.Second)[0]
	intake.mu.Lock()
	calls := intake.calls
	intake.mu.Unlock()
	if calls < 2 {
		t.Fatalf("expected a retry after failure, calls=%d", calls)
	}
	if _, err := os.Stat(p.FrontPath); err != nil {
		t.Fatalf("archived front missing after retry: %v", err)
	}
}

func TestBatchFromPrefix(t *testing.T) {
	tests := map[string]string{"B1_001": "B1", "20261105-SCAN_0003": "20261105-SCAN", "SCAN": "SCAN", "a/b_1": "a-b", "_x": "_x"}
	for in, want := range tests {
		if got := batchFromPrefix(in); got != want {
			t.Errorf("batchFromPrefix(%q) = %q want %q", in, got, want)
		}
	}
	if _, err := New(Config{}, &fakeIntake{}); err == nil {
		t.Fatal("expected config error")
	}
}
