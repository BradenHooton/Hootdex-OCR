package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hootyeah/hootdex/internal/application"
	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

const maxUploadBytes = 64 << 20

// Deps wires the handlers to application services.
type Deps struct {
	Review    *application.ReviewService
	Inventory *application.InventoryService
	Intake    ports.ScanIntake
	Broker    *Broker
	Stats     func() domain.PipelineStats // pipeline worker stats (optional)
	Wake      func()                      // nudge workers after an upload (optional)
	UploadDir string                      // archive root for manual uploads
	WebDir    string                      // built frontend (web/dist); optional
	NewID     func() string
	Log       *slog.Logger
}

// NewHandler builds the router using Go 1.22+ method/wildcard patterns.
func NewHandler(d Deps) http.Handler {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	s := &server{d: d}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("GET /api/v1/jobs/stream", d.Broker)
	mux.HandleFunc("GET /api/v1/stats", s.stats)
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.getJob)
	mux.HandleFunc("GET /api/v1/jobs/{id}/images/{name}", s.jobImage)
	mux.HandleFunc("GET /api/v1/review-queue", s.listReview)
	mux.HandleFunc("GET /api/v1/review-queue/{id}", s.getReview)
	mux.HandleFunc("POST /api/v1/review-queue/{id}/approve", s.approve)
	mux.HandleFunc("POST /api/v1/review-queue/{id}/reject", s.reject)
	mux.HandleFunc("GET /api/v1/inventory", s.listInventory)
	mux.HandleFunc("GET /api/v1/inventory/{id}", s.getInventory)
	mux.HandleFunc("GET /api/v1/storage", s.storage)
	mux.HandleFunc("GET /api/v1/checklist/search", s.searchChecklist)
	mux.HandleFunc("POST /api/v1/upload", s.upload)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, d.Log, fmt.Errorf("%w: no route %s %s", domain.ErrNotFound, r.Method, r.URL.Path))
	})
	if d.WebDir != "" {
		mux.Handle("/", spaHandler(d.WebDir))
	}
	return recoverer(d.Log, logRequests(d.Log, mux))
}

type server struct{ d Deps }

// --- jobs & stats ------------------------------------------------------------

func (s *server) stats(w http.ResponseWriter, r *http.Request) {
	counts, err := s.d.Inventory.QueueCounts(r.Context())
	if err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	resp := map[string]any{"queue": counts}
	if s.d.Stats != nil {
		resp["pipeline"] = s.d.Stats()
	}
	writeJSON(w, http.StatusOK, resp)
}

// SnapshotEvents builds the events sent to a new SSE subscriber.
func SnapshotEvents(inv *application.InventoryService, stats func() domain.PipelineStats) func(context.Context) []domain.PipelineEvent {
	return func(ctx context.Context) []domain.PipelineEvent {
		counts, err := inv.QueueCounts(ctx)
		if err != nil {
			return nil
		}
		ev := domain.PipelineEvent{Type: domain.EventQueueStats, Queue: counts, At: time.Now().UTC()}
		if stats != nil {
			st := stats()
			ev.Stats = &st
		}
		return []domain.PipelineEvent{ev}
	}
}

func (s *server) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.d.Inventory.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// jobImage serves a job's images by logical name; file paths come only from the DB, never the URL.
func (s *server) jobImage(w http.ResponseWriter, r *http.Request) {
	j, err := s.d.Inventory.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	name := r.PathValue("name")
	var path string
	switch name {
	case "front":
		path = firstNonEmpty(j.CroppedFrontPath, j.FrontImagePath)
	case "back":
		path = firstNonEmpty(j.CroppedBackPath, j.BackImagePath)
	case "front-original":
		path = j.FrontImagePath
	case "back-original":
		path = j.BackImagePath
	default:
		pre, err := application.DecodePreprocessMeta(j)
		if err != nil {
			writeError(w, r, s.d.Log, err)
			return
		}
		if pre != nil {
			if z, ok := pre.Zone(domain.ZoneName(strings.ToUpper(name))); ok {
				path = z.Path
			}
		}
	}
	if path == "" {
		writeError(w, r, s.d.Log, fmt.Errorf("%w: image %q for job %s", domain.ErrNotFound, name, j.ID))
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeError(w, r, s.d.Log, fmt.Errorf("%w: %v", domain.ErrNotFound, err))
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeContent(w, r, filepath.Base(path), fi.ModTime(), f)
}

// --- review queue ------------------------------------------------------------

func (s *server) listReview(w http.ResponseWriter, r *http.Request) {
	items, err := s.d.Review.List(r.Context(), atoiDefault(r.URL.Query().Get("limit"), 100))
	if err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (s *server) getReview(w http.ResponseWriter, r *http.Request) {
	item, err := s.d.Review.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *server) approve(w http.ResponseWriter, r *http.Request) {
	var in application.ApproveInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	rec, err := s.d.Review.Approve(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, toCardDTO(*rec))
}

func (s *server) reject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, r, s.d.Log, err)
			return
		}
	}
	if err := s.d.Review.Reject(r.Context(), r.PathValue("id"), in.Reason); err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- inventory -----------------------------------------------------------------

func (s *server) listInventory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := domain.InventoryFilter{
		Category: domain.Category(strings.ToUpper(q.Get("category"))), ReleaseYear: atoiDefault(q.Get("year"), 0),
		MinHootScore: atofDefault(q.Get("min_hoot")), MaxHootScore: atofDefault(q.Get("max_hoot")),
		BoxCode: q.Get("box"), Status: domain.InventoryStatus(strings.ToUpper(q.Get("status"))), Search: q.Get("q"),
		Limit: atoiDefault(q.Get("limit"), 200), Offset: atoiDefault(q.Get("offset"), 0),
	}
	if f.Category != "" && !f.Category.Valid() {
		writeError(w, r, s.d.Log, fmt.Errorf("%w: category %q", domain.ErrInvalid, f.Category))
		return
	}
	if f.Status != "" && !f.Status.Valid() {
		writeError(w, r, s.d.Log, fmt.Errorf("%w: status %q", domain.ErrInvalid, f.Status))
		return
	}
	recs, err := s.d.Inventory.List(r.Context(), f)
	if err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	items := make([]cardDTO, 0, len(recs))
	for _, rec := range recs {
		items = append(items, toCardDTO(rec))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (s *server) getInventory(w http.ResponseWriter, r *http.Request) {
	rec, snaps, err := s.d.Inventory.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"card": toCardDTO(*rec), "price_history": snaps})
}

func (s *server) storage(w http.ResponseWriter, r *http.Request) {
	locs, err := s.d.Inventory.Storage(r.Context())
	if err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": locs})
}

func (s *server) searchChecklist(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cands, err := s.d.Inventory.SearchChecklist(r.Context(), q.Get("q"), atoiDefault(q.Get("year"), 0), domain.Category(strings.ToUpper(q.Get("category"))))
	if err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": cands})
}

// --- manual upload -------------------------------------------------------------

func (s *server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeError(w, r, s.d.Log, fmt.Errorf("%w: multipart: %v", domain.ErrInvalid, err))
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	batch := "UPLOAD-" + time.Now().UTC().Format("20060102")
	dir := filepath.Join(s.d.UploadDir, batch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	prefix := s.d.NewID()
	front, err := saveUpload(r, "front", dir, prefix)
	if err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	if front == "" {
		writeError(w, r, s.d.Log, fmt.Errorf("%w: multipart field \"front\" is required", domain.ErrInvalid))
		return
	}
	back, err := saveUpload(r, "back", dir, prefix)
	if err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	job, err := s.d.Intake.EnqueueScan(r.Context(), domain.ScanPair{BatchID: batch, Prefix: prefix, FrontPath: front, BackPath: back})
	if err != nil {
		writeError(w, r, s.d.Log, err)
		return
	}
	if s.d.Wake != nil {
		s.d.Wake()
	}
	writeJSON(w, http.StatusAccepted, job)
}

var allowedImageTypes = map[string]string{"image/jpeg": ".jpg", "image/png": ".png"}

// saveUpload stores one multipart image (sniffed content type, server-chosen file name).
func saveUpload(r *http.Request, field, dir, prefix string) (string, error) {
	f, hdr, err := r.FormFile(field)
	if errors.Is(err, http.ErrMissingFile) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", domain.ErrInvalid, field, err)
	}
	defer f.Close()
	ext, err := sniffImage(f, hdr)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", domain.ErrInvalid, field, err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%s_%s%s", prefix, field, ext))
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, f); err != nil {
		_ = out.Close()
		return "", err
	}
	return path, out.Close()
}

func sniffImage(f multipart.File, hdr *multipart.FileHeader) (string, error) {
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	ct := http.DetectContentType(head[:n])
	if ext, ok := allowedImageTypes[ct]; ok {
		return ext, nil
	}
	if e := strings.ToLower(filepath.Ext(hdr.Filename)); (e == ".tif" || e == ".tiff") && n >= 4 &&
		(string(head[:4]) == "II*\x00" || string(head[:4]) == "MM\x00*") {
		return ".tif", nil
	}
	return "", fmt.Errorf("unsupported image type %s", ct)
}

// --- helpers -------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError maps domain errors onto HTTP status codes.
func writeError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, domain.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrNoStorageCapacity):
		status = http.StatusConflict
	case errors.Is(err, context.Canceled):
		status = 499
	}
	msg := err.Error()
	if status == http.StatusInternalServerError {
		log.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		msg = "internal error"
	}
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: request body: %v", domain.ErrInvalid, err)
	}
	return nil
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func atofDefault(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) { s.status = code; s.ResponseWriter.WriteHeader(code) }

// Flush keeps SSE working through the logging wrapper.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.DebugContext(r.Context(), "http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "dur", time.Since(start))
	})
}

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				writeError(w, r, log, fmt.Errorf("panic: %v", v))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// spaHandler serves the built React app, falling back to index.html for client routes.
func spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if fi, err := os.Stat(p); err != nil || fi.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	})
}
