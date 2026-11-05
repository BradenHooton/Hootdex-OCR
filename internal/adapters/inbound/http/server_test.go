package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	httpapi "github.com/hootyeah/hootdex/internal/adapters/inbound/http"
	"github.com/hootyeah/hootdex/internal/adapters/outbound/market"
	"github.com/hootyeah/hootdex/internal/adapters/outbound/sqlite"
	"github.com/hootyeah/hootdex/internal/adapters/outbound/vision"
	"github.com/hootyeah/hootdex/internal/application"
	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/platform/uuid"
	"github.com/hootyeah/hootdex/internal/seed"
	"github.com/hootyeah/hootdex/internal/testutil/synthcard"
)

var stroudZones = map[domain.ZoneName]string{
	domain.ZoneFrontHeader: "PRI2M", domain.ZoneFrontFooter: "C.J. STR0UD [RC] HOUSTON TEXANS",
	domain.ZoneBackHeader: "#339 C.J. STROUD QB PRIZM", domain.ZoneBackFooter: "©2023 Panini America, Inc.",
}

type harness struct {
	srv       *httptest.Server
	db        *sqlite.DB
	jobs      *sqlite.JobRepository
	storage   *sqlite.StorageRepository
	checklist *sqlite.ChecklistRepository
	pipe      *application.Pipeline
	data      string
}

// newHarness wires the real adapters exactly like cmd/server, with OCR replayed from zones.
func newHarness(t *testing.T, seedStorage bool, zones map[domain.ZoneName]string) *harness {
	t.Helper()
	ctx := context.Background()
	data := t.TempDir()
	db, err := sqlite.Open(ctx, filepath.Join(data, "hootdex.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h := &harness{db: db, jobs: sqlite.NewJobRepository(db), storage: sqlite.NewStorageRepository(db), checklist: sqlite.NewChecklistRepository(db), data: data}
	if seedStorage {
		if _, err := seed.Apply(ctx, h.checklist, h.storage); err != nil {
			t.Fatal(err)
		}
	} else {
		for _, e := range seed.Checklist() {
			e := e
			if err := h.checklist.Upsert(ctx, &e); err != nil {
				t.Fatal(err)
			}
		}
	}
	inventory := sqlite.NewInventoryRepository(db)
	invSvc := application.NewInventoryService(inventory, h.storage, h.checklist, h.jobs)
	valuer := application.NewValuationService(market.NewPriceService(nil), 0)
	rec := vision.RecognizerFunc(func(context.Context, string, domain.ZoneName) (string, error) { return "", nil })
	if zones != nil {
		rec = func(_ context.Context, _ string, z domain.ZoneName) (string, error) { return zones[z], nil }
	}
	broker := httpapi.NewBroker(nil, nil)
	h.pipe = application.NewPipeline(application.PipelineDeps{
		Jobs: h.jobs, Inventory: inventory, Valuer: valuer, Events: broker, NewID: uuid.New,
		Preprocess: application.NewPreprocessService(h.jobs, vision.NewPreprocessor(nil), filepath.Join(data, "processed"), broker, nil),
		Extractor:  application.NewExtractionRouter(vision.NewTesseractExtractor(rec, h.checklist, nil), false, nil),
	}, application.PipelineConfig{Workers: 2})
	broker.SetSnapshot(httpapi.SnapshotEvents(invSvc, h.pipe.Stats))
	handler := httpapi.NewHandler(httpapi.Deps{
		Review:    application.NewReviewService(h.jobs, inventory, h.checklist, valuer, broker, uuid.New, nil),
		Inventory: invSvc, Intake: application.NewIngestionService(h.jobs, broker, uuid.New, nil), Broker: broker,
		Stats: h.pipe.Stats, UploadDir: filepath.Join(data, "archive"), NewID: uuid.New,
	})
	h.srv = httptest.NewServer(handler)
	t.Cleanup(h.srv.Close)
	return h
}

func (h *harness) getJSON(t *testing.T, path string, wantStatus int, out any) {
	t.Helper()
	resp, err := http.Get(h.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s = %d want %d: %s", path, resp.StatusCode, wantStatus, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("decode %s: %v (%s)", path, err, body)
		}
	}
}

func (h *harness) postJSON(path string, body any) (int, []byte) {
	buf, _ := json.Marshal(body)
	resp, err := http.Post(h.srv.URL+path, "application/json", bytes.NewReader(buf))
	if err != nil {
		return 0, []byte(err.Error())
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

type sseEvent struct {
	name string
	data domain.PipelineEvent
}

// subscribe opens the SSE stream and collects events in the background.
func (h *harness) subscribe(t *testing.T) (func() []sseEvent, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.srv.URL+"/api/v1/jobs/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	var mu sync.Mutex
	var events []sseEvent
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		var name string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				var ev domain.PipelineEvent
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) == nil {
					mu.Lock()
					events = append(events, sseEvent{name, ev})
					mu.Unlock()
				}
			}
		}
	}()
	return func() []sseEvent {
			mu.Lock()
			defer mu.Unlock()
			return append([]sseEvent(nil), events...)
		}, func() {
			cancel()
			<-done
		}
}

func multipartBody(t *testing.T, files map[string][]byte) (*bytes.Buffer, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for field, data := range files {
		fw, err := mw.CreateFormFile(field, field+".jpg")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(data)
	}
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func renderJPEG(t *testing.T, s synthcard.Spec) []byte {
	p := filepath.Join(t.TempDir(), "x.jpg")
	if err := synthcard.Save(synthcard.Render(s), p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	return b
}

func TestUploadPipelineSSEAndInventory(t *testing.T) {
	h := newHarness(t, true, stroudZones)
	events, stop := h.subscribe(t)
	defer stop()

	body, ct := multipartBody(t, map[string][]byte{"front": renderJPEG(t, synthcard.Front()), "back": renderJPEG(t, synthcard.Back())})
	resp, err := http.Post(h.srv.URL+"/api/v1/upload", ct, body)
	if err != nil {
		t.Fatal(err)
	}
	var job domain.IngestionJob
	_ = json.NewDecoder(resp.Body).Decode(&job)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || job.Status != domain.JobPending || !strings.HasPrefix(job.BatchID, "UPLOAD-") {
		t.Fatalf("upload %d %+v", resp.StatusCode, job)
	}
	if err := h.pipe.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}

	// SSE: snapshot, queued, status updates and the completion with SKU, in order.
	var got []string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got = got[:0]
		var completed *domain.PipelineEvent
		for _, e := range events() {
			got = append(got, e.name)
			if e.name == string(domain.EventJobCompleted) {
				ev := e.data
				completed = &ev
			}
		}
		if completed != nil {
			if completed.SKU != "HY-FB-2023-PRZ-339-001" || completed.JobID != job.ID {
				t.Fatalf("completed event %+v", completed)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(got) < 4 || got[0] != string(domain.EventQueueStats) || got[1] != string(domain.EventJobQueued) || !contains(got, string(domain.EventJobCompleted)) {
		t.Fatalf("sse sequence %v", got)
	}

	var inv struct {
		Items []map[string]any `json:"items"`
	}
	h.getJSON(t, "/api/v1/inventory?category=football&min_hoot=1", http.StatusOK, &inv)
	if len(inv.Items) != 1 {
		t.Fatalf("inventory %d", len(inv.Items))
	}
	card := inv.Items[0]
	loc := card["location"].(map[string]any)
	meta := card["marketplace_meta"].(map[string]any)
	if card["sku"] != "HY-FB-2023-PRZ-339-001" || loc["id"] != "VAULT_A-CASE_01-BOX_01-ROW_1-S001" || card["market_price_cents"].(float64) <= 0 ||
		!strings.HasPrefix(meta["ebay_title"].(string), "2023 Panini Prizm #339 C.J. Stroud Silver Prizm RC") {
		t.Fatalf("card %v", card)
	}
	var detail struct {
		PriceHistory []domain.PriceQuote `json:"price_history"`
	}
	h.getJSON(t, "/api/v1/inventory/HY-FB-2023-PRZ-339-001", http.StatusOK, &detail)
	if len(detail.PriceHistory) != 1 {
		t.Fatalf("price history %+v", detail.PriceHistory)
	}

	for name, wantCT := range map[string]string{"front": "image/jpeg", "back": "image/jpeg", "FRONT_HEADER": "image/png", "front-original": "image/jpeg"} {
		r, err := http.Get(fmt.Sprintf("%s/api/v1/jobs/%s/images/%s", h.srv.URL, job.ID, name))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusOK || r.Header.Get("Content-Type") != wantCT {
			t.Fatalf("image %s: %d %s", name, r.StatusCode, r.Header.Get("Content-Type"))
		}
	}
	h.getJSON(t, "/api/v1/jobs/"+job.ID+"/images/SECRET", http.StatusNotFound, nil)

	var stats struct {
		Queue    map[string]int       `json:"queue"`
		Pipeline domain.PipelineStats `json:"pipeline"`
	}
	h.getJSON(t, "/api/v1/stats", http.StatusOK, &stats)
	if stats.Queue["COMPLETED"] != 1 || stats.Pipeline.Workers != 2 || stats.Pipeline.Processed != 1 {
		t.Fatalf("stats %+v", stats)
	}
}

// reviewJob inserts a NEEDS_REVIEW job carrying a low-confidence payload.
func (h *harness) reviewJob(t *testing.T, player string, conf float64) string {
	t.Helper()
	ctx := context.Background()
	j, _ := domain.NewIngestionJob(uuid.New(), "REV", "/scan/front.jpg", "")
	if err := h.jobs.Create(ctx, j); err != nil {
		t.Fatal(err)
	}
	c, err := h.jobs.ClaimNextPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := domain.CardExtractionPayload{Category: domain.CategoryFootball, ReleaseYear: 2023, Manufacturer: "Panini", SetName: "Prizm",
		CardNumber: "339", PlayerOrCharacter: player, ParallelVariant: "Base", GradingCompany: domain.GraderRaw, ConfidenceScore: conf}
	b, _ := json.Marshal(p)
	c.Status, c.ExtractedPayloadJSON, c.ConfidenceScore, c.RawOCRFront = domain.JobNeedsReview, string(b), conf, "PR1ZM #339 "+player
	if err := h.jobs.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func TestReviewQueueApproveIsTransactionalUnderConcurrency(t *testing.T) {
	h := newHarness(t, false, nil)
	ctx := context.Background()
	for slot := 1; slot <= 2; slot++ { // two slots, capacity 1 each
		l, _ := domain.NewStorageLocation("VAULT_A", "CASE_01", "BOX_01", "ROW_1", slot, 1)
		if err := h.storage.Upsert(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{h.reviewJob(t, "CJ STR0UD", 0.6), h.reviewJob(t, "CJ STROUD", 0.7), h.reviewJob(t, "C STROUD", 0.5)}

	var queue struct {
		Items []application.ReviewItem `json:"items"`
		Count int                      `json:"count"`
	}
	h.getJSON(t, "/api/v1/review-queue", http.StatusOK, &queue)
	if queue.Count != 3 || len(queue.Items[0].Candidates) == 0 || queue.Items[0].Candidates[0].Entry.PlayerOrCharacter != "C.J. Stroud" {
		t.Fatalf("queue %+v", queue)
	}
	if !contains(queue.Items[0].LowConfidenceFields, "player_or_character") {
		t.Fatalf("low-confidence fields %v", queue.Items[0].LowConfidenceFields)
	}
	checklistID := queue.Items[0].Candidates[0].Entry.ID

	type result struct {
		id     string
		status int
		body   []byte
	}
	results := make(chan result, len(ids))
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			in := application.ApproveInput{ChecklistID: &checklistID, PlayerImpactTier: 2, AcquisitionCostCents: 500,
				Payload: domain.CardExtractionPayload{Category: domain.CategoryFootball, ReleaseYear: 2023, Manufacturer: "Panini",
					SetName: "Prizm", CardNumber: "339", PlayerOrCharacter: "C.J. Stroud", ParallelVariant: "Silver Prizm", GradingCompany: "RAW"}}
			st, b := h.postJSON("/api/v1/review-queue/"+id+"/approve", in)
			results <- result{id, st, b}
		}(id)
	}
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	var rejected string
	skus := map[string]bool{}
	for r := range results {
		switch r.status {
		case http.StatusOK:
			ok++
			var c map[string]any
			_ = json.Unmarshal(r.body, &c)
			skus[c["sku"].(string)] = true
			if c["inventory_status"] != "IN_STOCK" || c["player_impact_tier"].(float64) != 2 || c["hoot_score"].(float64) <= 0 {
				t.Fatalf("approved card %v", c)
			}
		case http.StatusConflict:
			conflict++
			rejected = r.id
		default:
			t.Fatalf("approve %s: %d %s", r.id, r.status, r.body)
		}
	}
	if ok != 2 || conflict != 1 || !skus["HY-FB-2023-PRZ-339-001"] || !skus["HY-FB-2023-PRZ-339-002"] {
		t.Fatalf("ok=%d conflict=%d skus=%v", ok, conflict, skus)
	}
	locs, _ := h.storage.List(ctx)
	for _, l := range locs {
		if l.OccupiedCount != 1 {
			t.Fatalf("slot %s occupied %d (over/under allocated)", l.ID, l.OccupiedCount)
		}
	}
	j, _ := h.jobs.Get(ctx, rejected)
	if j.Status != domain.JobNeedsReview {
		t.Fatalf("failed approval must leave the job in review, got %s", j.Status)
	}

	// Approving a completed job is a conflict; bad payloads are 400; reject clears the queue.
	if st, _ := h.postJSON("/api/v1/review-queue/"+ids[0]+"/approve", application.ApproveInput{}); st != http.StatusConflict && st != http.StatusBadRequest {
		t.Fatalf("re-approve status %d", st)
	}
	if st, b := h.postJSON("/api/v1/review-queue/"+rejected+"/approve", application.ApproveInput{Payload: domain.CardExtractionPayload{Category: "FOOTBALL"}}); st != http.StatusBadRequest {
		t.Fatalf("incomplete payload: %d %s", st, b)
	}
	if st, b := h.postJSON("/api/v1/review-queue/"+rejected+"/reject", map[string]string{"reason": "double feed"}); st != http.StatusNoContent {
		t.Fatalf("reject: %d %s", st, b)
	}
	h.getJSON(t, "/api/v1/review-queue", http.StatusOK, &queue)
	if queue.Count != 0 {
		t.Fatalf("queue not empty: %d", queue.Count)
	}
}

func TestAPIErrorsAndLookups(t *testing.T) {
	h := newHarness(t, true, nil)
	h.getJSON(t, "/api/v1/inventory?category=cricket", http.StatusBadRequest, nil)
	h.getJSON(t, "/api/v1/inventory/nope", http.StatusNotFound, nil)
	h.getJSON(t, "/api/v1/jobs/nope", http.StatusNotFound, nil)
	h.getJSON(t, "/api/v1/review-queue/nope", http.StatusNotFound, nil)
	h.getJSON(t, "/api/v1/unknown", http.StatusNotFound, nil)
	var storage struct {
		Items []domain.StorageLocation `json:"items"`
	}
	h.getJSON(t, "/api/v1/storage", http.StatusOK, &storage)
	if len(storage.Items) != 20 {
		t.Fatalf("storage %d", len(storage.Items))
	}
	var search struct {
		Items []domain.ChecklistCandidate `json:"items"`
	}
	h.getJSON(t, "/api/v1/checklist/search?q=charizard+4/102", http.StatusOK, &search)
	if len(search.Items) == 0 || search.Items[0].Entry.PlayerOrCharacter != "Charizard" {
		t.Fatalf("search %+v", search.Items)
	}
	body, ct := multipartBody(t, map[string][]byte{"front": []byte("not an image at all")})
	resp, err := http.Post(h.srv.URL+"/api/v1/upload", ct, body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-image upload: %d", resp.StatusCode)
	}
	if st, _ := h.postJSON("/api/v1/review-queue/x/approve", map[string]any{"unknown_field": 1}); st != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", st)
	}
	r, _ := http.Get(h.srv.URL + "/healthz")
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatal("healthz")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
