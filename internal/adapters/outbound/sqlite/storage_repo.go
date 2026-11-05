package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// StorageRepository implements ports.StorageRepository.
type StorageRepository struct{ db *DB }

var _ ports.StorageRepository = (*StorageRepository)(nil)

// NewStorageRepository constructs the repository.
func NewStorageRepository(db *DB) *StorageRepository { return &StorageRepository{db: db} }

const storageCols = `id, vault_code, case_code, box_code, row_code, slot_number, capacity, occupied_count, barcode_label, created_at`

// fillOrder mirrors domain.LocationLess.
const fillOrder = `ORDER BY vault_code, case_code, box_code, row_code, slot_number`

type rowScanner interface{ Scan(dest ...any) error }

func scanLocation(r rowScanner) (domain.StorageLocation, error) {
	var l domain.StorageLocation
	err := r.Scan(&l.ID, &l.VaultCode, &l.CaseCode, &l.BoxCode, &l.RowCode, &l.SlotNumber, &l.Capacity, &l.OccupiedCount, &l.BarcodeLabel, &l.CreatedAt)
	return l, err
}

// Upsert inserts the location or updates its capacity / barcode (occupancy is preserved).
func (r *StorageRepository) Upsert(ctx context.Context, loc domain.StorageLocation) error {
	if err := loc.Validate(); err != nil {
		return fmt.Errorf("sqlite.StorageRepository.Upsert [%s]: %w", loc.ID, err)
	}
	if loc.ID == "" {
		loc.ID = loc.Slug()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO storage_locations
		(id, vault_code, case_code, box_code, row_code, slot_number, capacity, occupied_count, barcode_label)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET capacity = excluded.capacity, barcode_label = excluded.barcode_label`,
		loc.ID, loc.VaultCode, loc.CaseCode, loc.BoxCode, loc.RowCode, loc.SlotNumber, loc.Capacity, loc.OccupiedCount, loc.BarcodeLabel)
	if err != nil {
		return fmt.Errorf("sqlite.StorageRepository.Upsert [%s]: %w", loc.ID, err)
	}
	return nil
}

// Get loads one location.
func (r *StorageRepository) Get(ctx context.Context, id string) (domain.StorageLocation, error) {
	l, err := scanLocation(r.db.QueryRowContext(ctx, `SELECT `+storageCols+` FROM storage_locations WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return l, fmt.Errorf("sqlite.StorageRepository.Get [%s]: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return l, fmt.Errorf("sqlite.StorageRepository.Get [%s]: %w", id, err)
	}
	return l, nil
}

// List returns every location in fill order.
func (r *StorageRepository) List(ctx context.Context) ([]domain.StorageLocation, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+storageCols+` FROM storage_locations `+fillOrder)
	if err != nil {
		return nil, fmt.Errorf("sqlite.StorageRepository.List: %w", err)
	}
	defer rows.Close()
	var out []domain.StorageLocation
	for rows.Next() {
		l, err := scanLocation(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite.StorageRepository.List: scan: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite.StorageRepository.List: %w", err)
	}
	return out, nil
}

// AllocateNext reserves capacity in its own transaction.
func (r *StorageRepository) AllocateNext(ctx context.Context) (domain.StorageLocation, error) {
	var loc domain.StorageLocation
	err := r.db.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		loc, err = allocateNextTx(ctx, tx)
		return err
	})
	if err != nil {
		return loc, fmt.Errorf("sqlite.StorageRepository.AllocateNext: %w", err)
	}
	return loc, nil
}

// allocateNextTx picks the first open slot and increments occupied_count inside tx.
func allocateNextTx(ctx context.Context, tx *sql.Tx) (domain.StorageLocation, error) {
	loc, err := scanLocation(tx.QueryRowContext(ctx, `SELECT `+storageCols+` FROM storage_locations
		WHERE occupied_count < capacity `+fillOrder+` LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return loc, domain.ErrNoStorageCapacity
	}
	if err != nil {
		return loc, fmt.Errorf("select open slot: %w", err)
	}
	if err := loc.Occupy(); err != nil {
		return loc, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE storage_locations SET occupied_count = occupied_count + 1
		WHERE id = ? AND occupied_count < capacity`, loc.ID)
	if err != nil {
		return loc, fmt.Errorf("occupy %s: %w", loc.ID, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return loc, fmt.Errorf("occupy %s: %w", loc.ID, domain.ErrConflict)
	}
	return loc, nil
}
