package application_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fswatch "github.com/hootyeah/hootdex/internal/adapters/inbound/fsnotify"
	"github.com/hootyeah/hootdex/internal/adapters/outbound/sqlite"
	"github.com/hootyeah/hootdex/internal/adapters/outbound/vision"
	"github.com/hootyeah/hootdex/internal/application"
	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/platform/uuid"
	"github.com/hootyeah/hootdex/internal/testutil/synthcard"
)

// TestSprint2Gate drops synthetic duplex scans into <data>/inbox and verifies the watcher
// pairs them, the preprocessor writes cropped + binarized zones to <data>/processed,
// centering ratios are calculated, and the SQLite job status advances.
// The ./data layout is mirrored under a temp root so tests never touch real shop data.
func TestSprint2Gate(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	inbox, archive, processed := filepath.Join(data, "inbox"), filepath.Join(data, "archive"), filepath.Join(data, "processed")
	for _, d := range []string{inbox, archive, processed} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := sqlite.Open(ctx, filepath.Join(data, "hootdex.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	jobs := sqlite.NewJobRepository(db)
	pub := &recordingPublisher{}
	intake := application.NewIngestionService(jobs, pub, uuid.New, nil)
	w, err := fswatch.New(fswatch.Config{InboxDir: inbox, ArchiveDir: archive}, intake)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = w.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)

	// Render to a staging dir then move in, as the scanner driver would.
	stage := t.TempDir()
	for name, spec := range map[string]synthcard.Spec{"HY20261105_0001_front.jpg": synthcard.Front(), "HY20261105_0001_back.jpg": synthcard.Back()} {
		p := filepath.Join(stage, name)
		if err := synthcard.Save(synthcard.Render(spec), p); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(p, filepath.Join(inbox, name)); err != nil {
			t.Fatal(err)
		}
	}

	var job *domain.IngestionJob
	deadline := time.Now().Add(10 * time.Second)
	for job == nil && time.Now().Before(deadline) {
		if pending, _ := jobs.ListByStatus(ctx, domain.JobPending, 10); len(pending) == 1 {
			job = pending[0]
		}
		time.Sleep(25 * time.Millisecond)
	}
	if job == nil {
		t.Fatal("watcher never enqueued the pair")
	}
	if job.BatchID != "HY20261105" || !strings.HasPrefix(job.FrontImagePath, filepath.Join(archive, "HY20261105")) || job.BackImagePath == "" {
		t.Fatalf("job not paired/archived: %+v", job)
	}

	claimed, err := jobs.ClaimNextPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	svc := application.NewPreprocessService(jobs, vision.NewPreprocessor(nil), processed, pub, nil)
	res, err := svc.Run(ctx, claimed)
	if err != nil {
		t.Fatal(err)
	}

	stored, err := jobs.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.JobExtracting || stored.CroppedFrontPath == "" || stored.CroppedBackPath == "" {
		t.Fatalf("job not advanced: %+v", stored)
	}
	meta, err := application.DecodePreprocessMeta(stored)
	if err != nil || meta == nil {
		t.Fatalf("meta: %v", err)
	}
	var lr, tb int
	if _, err := fmt.Sscanf(meta.CenteringLR, "%d/", &lr); err != nil || lr < 55 || lr > 59 {
		t.Fatalf("centering_lr %q", meta.CenteringLR)
	}
	if _, err := fmt.Sscanf(meta.CenteringTB, "%d/", &tb); err != nil || tb < 48 || tb > 52 {
		t.Fatalf("centering_tb %q", meta.CenteringTB)
	}
	jobDir := filepath.Join(processed, job.ID)
	for _, z := range []domain.ZoneName{domain.ZoneFrontHeader, domain.ZoneFrontFooter, domain.ZoneBackHeader, domain.ZoneBackFooter} {
		zi, ok := res.Zone(z)
		if !ok || filepath.Dir(zi.Path) != jobDir {
			t.Fatalf("zone %s missing from %s", z, jobDir)
		}
		if fi, err := os.Stat(zi.Path); err != nil || fi.Size() == 0 {
			t.Fatalf("zone file %s: %v", zi.Path, err)
		}
	}
	if types := pub.types(); len(types) < 2 || types[0] != domain.EventJobQueued || types[len(types)-1] != domain.EventJobStatus {
		t.Fatalf("events %v", types)
	}
}
