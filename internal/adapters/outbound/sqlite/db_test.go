package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/platform/uuid"
)

// openTestDB opens a real temporary-file SQLite database initialised with the schema migrations.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "hootdex.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenAppliesPragmasOnEveryConnection(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	// Hold several connections at once so the pool must open more than one.
	conns := make([]interface{ Close() error }, 0, 4)
	for i := 0; i < 4; i++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, conn)
		var mode string
		var fk, timeout, sync int
		if err := conn.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&timeout); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&sync); err != nil {
			t.Fatal(err)
		}
		if mode != "wal" || fk != 1 || timeout != 5000 || sync != 1 {
			t.Fatalf("conn %d pragmas: journal=%s fk=%d busy=%d sync=%d", i, mode, fk, timeout, sync)
		}
	}
	for _, c := range conns {
		_ = c.Close()
	}
}

func TestMigrateIsIdempotentAndCreatesFTSTriggers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	ctx := context.Background()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("schema_migrations = %d (%v), want 2", n, err)
	}
	for _, trig := range []string{"master_checklists_ai", "master_checklists_ad", "master_checklists_au"} {
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, trig).Scan(&n); err != nil || n != 1 {
			t.Fatalf("trigger %s missing", trig)
		}
	}
}

func TestFTSTriggersKeepIndexInSync(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	count := func(q string) int {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM master_checklists_fts WHERE master_checklists_fts MATCH ?`, q).Scan(&n); err != nil {
			t.Fatalf("match %q: %v", q, err)
		}
		return n
	}
	res, err := db.ExecContext(ctx, `INSERT INTO master_checklists (category, release_year, manufacturer, set_name, card_number, player_or_character)
		VALUES ('FOOTBALL', 2023, 'Panini', 'Prizm', '339', 'C.J. Stroud')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if count("stroud") != 1 {
		t.Fatal("insert trigger did not index row")
	}
	if _, err := db.ExecContext(ctx, `UPDATE master_checklists SET player_or_character = 'Bryce Young' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if count("stroud") != 0 || count("young") != 1 {
		t.Fatal("update trigger did not re-index row")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM master_checklists WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if count("young") != 0 {
		t.Fatal("delete trigger left phantom FTS row")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO master_checklists_fts(master_checklists_fts) VALUES('integrity-check')`); err != nil {
		t.Fatalf("fts integrity-check: %v", err)
	}
}

func seedLocations(t *testing.T, repo *StorageRepository, n, capacity int) []domain.StorageLocation {
	t.Helper()
	var out []domain.StorageLocation
	for i := 0; i < n; i++ {
		l, err := domain.NewStorageLocation("VAULT_A", "CASE_01", fmt.Sprintf("BOX_%02d", i/5+1), "ROW_1", i%5+1, capacity)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Upsert(context.Background(), l); err != nil {
			t.Fatal(err)
		}
		out = append(out, l)
	}
	return out
}

func TestStorageAllocateNextFillsInOrderAndStopsWhenFull(t *testing.T) {
	db := openTestDB(t)
	repo := NewStorageRepository(db)
	ctx := context.Background()
	seedLocations(t, repo, 3, 2)
	var got []string
	for i := 0; i < 6; i++ {
		l, err := repo.AllocateNext(ctx)
		if err != nil {
			t.Fatalf("allocate %d: %v", i, err)
		}
		got = append(got, l.ID)
	}
	want := []string{"S001", "S001", "S002", "S002", "S003", "S003"}
	for i := range want {
		if got[i][len(got[i])-4:] != want[i] {
			t.Fatalf("allocation order %v", got)
		}
	}
	if _, err := repo.AllocateNext(ctx); !errors.Is(err, domain.ErrNoStorageCapacity) {
		t.Fatalf("expected full, got %v", err)
	}
	all, _ := repo.List(ctx)
	for _, l := range all {
		if l.OccupiedCount != 2 {
			t.Fatalf("occupied %s = %d", l.ID, l.OccupiedCount)
		}
	}
	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestStorageAllocateNextConcurrent(t *testing.T) {
	db := openTestDB(t)
	repo := NewStorageRepository(db)
	seedLocations(t, repo, 4, 5) // 20 units
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, full := 0, 0
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.AllocateNext(context.Background())
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, domain.ErrNoStorageCapacity):
				full++
			default:
				t.Errorf("allocate: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != 20 || full != 5 {
		t.Fatalf("ok=%d full=%d, want 20/5", ok, full)
	}
}

func newJob(t *testing.T, repo *JobRepository) *domain.IngestionJob {
	t.Helper()
	j, err := domain.NewIngestionJob(uuid.New(), "B1", "/archive/B1/f.jpg", "/archive/B1/b.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestJobLifecycle(t *testing.T) {
	db := openTestDB(t)
	repo := NewJobRepository(db)
	ctx := context.Background()
	if _, err := repo.ClaimNextPending(ctx); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("empty queue: %v", err)
	}
	j1, j2 := newJob(t, repo), newJob(t, repo)
	c, err := repo.ClaimNextPending(ctx)
	if err != nil || c.ID != j1.ID || c.Status != domain.JobPreprocessing {
		t.Fatalf("claim: %+v %v", c, err)
	}
	c.CroppedFrontPath, c.RawOCRFront, c.ExtractorUsed, c.ConfidenceScore = "/p/f.png", "PRIZM", domain.ExtractorTesseract, 0.9
	c.PreprocessMetaJSON = `{"card_type":"RAW_CARD"}`
	if err := c.TransitionTo(domain.JobExtracting); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, j1.ID)
	if err != nil || got.Status != domain.JobExtracting || got.RawOCRFront != "PRIZM" || got.ExtractorUsed != domain.ExtractorTesseract ||
		got.PreprocessMetaJSON == "" || got.BackImagePath != "/archive/B1/b.jpg" || got.CreatedAt.IsZero() {
		t.Fatalf("get: %+v %v", got, err)
	}
	counts, _ := repo.CountByStatus(ctx)
	if counts[domain.JobPending] != 1 || counts[domain.JobExtracting] != 1 {
		t.Fatalf("counts %v", counts)
	}
	// Crash recovery: in-flight job goes back to PENDING with retry++.
	n, err := repo.RequeueInFlight(ctx)
	if err != nil || n != 1 {
		t.Fatalf("requeue: %d %v", n, err)
	}
	pending, _ := repo.ListByStatus(ctx, domain.JobPending, 10)
	if len(pending) != 2 || pending[0].ID != j1.ID || pending[0].RetryCount != 1 || pending[1].ID != j2.ID {
		t.Fatalf("pending after requeue: %+v", pending)
	}
	if err := repo.Update(ctx, &domain.IngestionJob{ID: "missing", Status: domain.JobFailed}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
}

func TestJobRequeueFailsExhaustedJobs(t *testing.T) {
	db := openTestDB(t)
	repo := NewJobRepository(db)
	ctx := context.Background()
	j := newJob(t, repo)
	c, _ := repo.ClaimNextPending(ctx)
	c.RetryCount = domain.MaxJobRetries
	if err := repo.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.RequeueInFlight(ctx); n != 0 {
		t.Fatalf("requeued exhausted job")
	}
	got, _ := repo.Get(ctx, j.ID)
	if got.Status != domain.JobFailed {
		t.Fatalf("status %s", got.Status)
	}
}

func TestJobClaimIsExclusiveUnderConcurrency(t *testing.T) {
	db := openTestDB(t)
	repo := NewJobRepository(db)
	for i := 0; i < 30; i++ {
		newJob(t, repo)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[string]int{}
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				j, err := repo.ClaimNextPending(context.Background())
				if errors.Is(err, domain.ErrNotFound) {
					return
				}
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				mu.Lock()
				seen[j.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 30 {
		t.Fatalf("claimed %d distinct jobs, want 30", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("job %s claimed %d times", id, n)
		}
	}
}

func sampleCard(jobID string) *domain.Card {
	return &domain.Card{
		ID: uuid.New(), IngestionJobID: jobID, Category: domain.CategoryFootball, ReleaseYear: 2023, Manufacturer: "Panini",
		SetName: "Prizm", CardNumber: "339", PlayerOrCharacter: "C.J. Stroud", TeamOrType: "Houston Texans",
		ParallelVariant: "Silver Prizm", IsRookie: true, GradingCompany: domain.GraderRaw, EstimatedRawCondition: domain.ConditionNMMT,
		PlayerImpactTier: 2, ScarcityMultiplier: 1, InventoryStatus: domain.StatusInStock, MarketPriceCents: 10000,
		HootScore: 78.5, FrontImageURL: "/archive/B1/f.jpg", MarketplaceMetaJSON: "{}",
	}
}

func TestInventoryFinalizeCard(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	storage, jobs, inv := NewStorageRepository(db), NewJobRepository(db), NewInventoryRepository(db)
	seedLocations(t, storage, 1, 2)

	j := newJob(t, jobs)
	claimed, _ := jobs.ClaimNextPending(ctx)
	for _, s := range []domain.JobStatus{domain.JobExtracting, domain.JobMatching} {
		_ = claimed.TransitionTo(s)
	}
	if err := jobs.Update(ctx, claimed); err != nil {
		t.Fatal(err)
	}

	c1 := sampleCard(j.ID)
	if err := inv.FinalizeCard(ctx, c1); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if c1.SKU != "HY-FB-2023-PRZ-339-001" || c1.StorageLocationID == "" {
		t.Fatalf("sku/slot: %s %s", c1.SKU, c1.StorageLocationID)
	}
	gotJob, _ := jobs.Get(ctx, j.ID)
	if gotJob.Status != domain.JobCompleted {
		t.Fatalf("job status %s", gotJob.Status)
	}
	// Finalizing again against the same (now completed) job must roll back entirely.
	dup := sampleCard(j.ID)
	if err := inv.FinalizeCard(ctx, dup); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if dup.SKU != "" || dup.StorageLocationID != "" {
		t.Fatal("rolled-back finalize leaked sku/slot")
	}
	loc, _ := storage.List(ctx)
	if loc[0].OccupiedCount != 1 {
		t.Fatalf("occupied after rollback = %d", loc[0].OccupiedCount)
	}

	c2 := sampleCard("")
	if err := inv.FinalizeCard(ctx, c2); err != nil {
		t.Fatal(err)
	}
	if c2.SKU != "HY-FB-2023-PRZ-339-002" {
		t.Fatalf("second sku %s", c2.SKU)
	}
	if err := inv.FinalizeCard(ctx, sampleCard("")); !errors.Is(err, domain.ErrNoStorageCapacity) {
		t.Fatalf("expected storage full, got %v", err)
	}

	rec, err := inv.Get(ctx, c1.SKU)
	if err != nil || rec.Card.ID != c1.ID || rec.Location == nil || rec.Location.BoxCode != "BOX_01" ||
		rec.Card.TeamOrType != "Houston Texans" || !rec.Card.IsRookie || rec.Card.CreatedAt.IsZero() {
		t.Fatalf("get: %+v %v", rec, err)
	}
	c1.MarketPriceCents, c1.HootScore, c1.MarketplaceMetaJSON = 12345, 81.2, `{"title":"x"}`
	if err := inv.UpdateValuation(ctx, c1); err != nil {
		t.Fatal(err)
	}

	list, err := inv.List(ctx, domain.InventoryFilter{Category: domain.CategoryFootball, BoxCode: "BOX_01", MinHootScore: 80})
	if err != nil || len(list) != 1 || list[0].Card.MarketPriceCents != 12345 {
		t.Fatalf("list: %d %v", len(list), err)
	}
	list, _ = inv.List(ctx, domain.InventoryFilter{Search: "stroud", Status: domain.StatusInStock})
	if len(list) != 2 {
		t.Fatalf("search list: %d", len(list))
	}
	list, _ = inv.List(ctx, domain.InventoryFilter{Category: domain.CategoryPokemon})
	if len(list) != 0 {
		t.Fatalf("category filter: %d", len(list))
	}
}

func TestInventoryFinalizeRejectsInvalidCard(t *testing.T) {
	db := openTestDB(t)
	inv := NewInventoryRepository(db)
	c := sampleCard("")
	c.PlayerOrCharacter = ""
	if err := inv.FinalizeCard(context.Background(), c); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("expected invalid, got %v", err)
	}
}

func TestFinalizeCardRecordsPriceSnapshot(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedLocations(t, NewStorageRepository(db), 1, 5)
	inv := NewInventoryRepository(db)
	c := sampleCard("")
	c.PendingPriceQuote = &domain.PriceQuote{PriceCents: 12500, Source: domain.PricePriceCharting, ListingURL: "https://example.test/p/1"}
	if err := inv.FinalizeCard(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := inv.AddPriceSnapshot(ctx, c.ID, domain.PriceQuote{PriceCents: 13000, Source: domain.PriceManual}); err != nil {
		t.Fatal(err)
	}
	snaps, err := inv.ListPriceSnapshots(ctx, c.ID)
	if err != nil || len(snaps) != 2 {
		t.Fatalf("snapshots %v %v", snaps, err)
	}
	if err := inv.AddPriceSnapshot(ctx, c.ID, domain.PriceQuote{PriceCents: 1, Source: "BOGUS"}); err == nil {
		t.Fatal("CHECK constraint on source must reject unknown sources")
	}
	if err := inv.AddPriceSnapshot(ctx, "missing-card", domain.PriceQuote{PriceCents: 1, Source: domain.PriceManual}); err == nil {
		t.Fatal("foreign key must reject unknown card")
	}
}

func TestFinalizeCardExplicitSlotRespectsCapacity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	locs := seedLocations(t, NewStorageRepository(db), 1, 1)
	inv := NewInventoryRepository(db)
	a, b := sampleCard(""), sampleCard("")
	a.StorageLocationID, b.StorageLocationID = locs[0].ID, locs[0].ID
	if err := inv.FinalizeCard(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := inv.FinalizeCard(ctx, b); !errors.Is(err, domain.ErrNoStorageCapacity) {
		t.Fatalf("expected full slot, got %v", err)
	}
	c := sampleCard("")
	c.StorageLocationID = "NOPE"
	if err := inv.FinalizeCard(ctx, c); !errors.Is(err, domain.ErrNoStorageCapacity) {
		t.Fatalf("expected unknown slot error, got %v", err)
	}
}
