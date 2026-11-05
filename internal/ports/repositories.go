// Package ports declares the hexagonal boundaries. It contains interfaces only.
package ports

import (
	"context"

	"github.com/hootyeah/hootdex/internal/domain"
)

// StorageRepository persists the 5-tier physical storage hierarchy.
type StorageRepository interface {
	Upsert(ctx context.Context, loc domain.StorageLocation) error
	Get(ctx context.Context, id string) (domain.StorageLocation, error)
	List(ctx context.Context) ([]domain.StorageLocation, error)
	// AllocateNext atomically reserves one unit of capacity in the first open slot
	// (canonical fill order) and returns it. Returns domain.ErrNoStorageCapacity when full.
	AllocateNext(ctx context.Context) (domain.StorageLocation, error)
}

// JobRepository is the durable ingestion queue.
type JobRepository interface {
	Create(ctx context.Context, job *domain.IngestionJob) error
	Get(ctx context.Context, id string) (*domain.IngestionJob, error)
	// ClaimNextPending atomically moves the oldest PENDING job to PREPROCESSING.
	// Returns domain.ErrNotFound when the queue is empty.
	ClaimNextPending(ctx context.Context) (*domain.IngestionJob, error)
	// Update persists every mutable column of the job.
	Update(ctx context.Context, job *domain.IngestionJob) error
	ListByStatus(ctx context.Context, status domain.JobStatus, limit int) ([]*domain.IngestionJob, error)
	CountByStatus(ctx context.Context) (map[domain.JobStatus]int, error)
	// RequeueInFlight resets jobs interrupted mid-flight (crash recovery) back to PENDING.
	RequeueInFlight(ctx context.Context) (int, error)
}

// InventoryRepository persists physical inventory cards.
type InventoryRepository interface {
	// FinalizeCard inserts the card in one transaction: it allocates the next storage slot
	// when card.StorageLocationID is empty, generates the SKU when card.SKU is empty and,
	// when card.IngestionJobID is set, marks that job COMPLETED. The card is updated in place.
	FinalizeCard(ctx context.Context, card *domain.Card) error
	Get(ctx context.Context, id string) (*domain.InventoryRecord, error)
	List(ctx context.Context, filter domain.InventoryFilter) ([]domain.InventoryRecord, error)
	UpdateValuation(ctx context.Context, card *domain.Card) error
	AddPriceSnapshot(ctx context.Context, cardID string, q domain.PriceQuote) error
	ListPriceSnapshots(ctx context.Context, cardID string) ([]domain.PriceQuote, error)
}

// ChecklistRepository is the ground-truth master checklist with fuzzy matching.
type ChecklistRepository interface {
	// Upsert inserts or updates an entry by its natural key and sets e.ID.
	Upsert(ctx context.Context, e *domain.ChecklistEntry) error
	Get(ctx context.Context, id int64) (*domain.ChecklistEntry, error)
	Count(ctx context.Context) (int, error)
	// MatchCandidates returns up to MaxCandidates entries ranked by fuzzy score (desc).
	// yearHint = 0 and categoryHint = "" disable the respective hint.
	MatchCandidates(ctx context.Context, rawText string, yearHint int, categoryHint domain.Category) ([]domain.ChecklistCandidate, error)
}
