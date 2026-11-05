package application_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hootyeah/hootdex/internal/adapters/outbound/sqlite"
	"github.com/hootyeah/hootdex/internal/application"
	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/platform/uuid"
)

type recordingPublisher struct {
	mu     sync.Mutex
	events []domain.PipelineEvent
}

func (r *recordingPublisher) Publish(_ context.Context, ev domain.PipelineEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingPublisher) types() []domain.EventType {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.EventType
	for _, e := range r.events {
		out = append(out, e.Type)
	}
	return out
}

func openDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "hootdex.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestEnqueueScanPersistsPendingJob(t *testing.T) {
	db := openDB(t)
	jobs := sqlite.NewJobRepository(db)
	pub := &recordingPublisher{}
	svc := application.NewIngestionService(jobs, pub, uuid.New, nil)
	job, err := svc.EnqueueScan(context.Background(), domain.ScanPair{BatchID: "B1", Prefix: "B1_0001", FrontPath: "/a/f.jpg", BackPath: "/a/b.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := jobs.Get(context.Background(), job.ID)
	if err != nil || got.Status != domain.JobPending || got.BackImagePath != "/a/b.jpg" {
		t.Fatalf("job: %+v %v", got, err)
	}
	if ts := pub.types(); len(ts) != 1 || ts[0] != domain.EventJobQueued {
		t.Fatalf("events %v", ts)
	}
	if _, err := svc.EnqueueScan(context.Background(), domain.ScanPair{BatchID: "B1"}); err == nil {
		t.Fatal("expected error for missing front image")
	}
}
