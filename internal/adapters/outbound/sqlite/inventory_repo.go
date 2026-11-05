package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// InventoryRepository implements ports.InventoryRepository over inventory_cards.
type InventoryRepository struct{ db *DB }

var _ ports.InventoryRepository = (*InventoryRepository)(nil)

// NewInventoryRepository constructs the repository.
func NewInventoryRepository(db *DB) *InventoryRepository { return &InventoryRepository{db: db} }

const cardCols = `c.id, c.sku, c.ingestion_job_id, c.checklist_id, c.storage_location_id,
	c.category, c.release_year, c.manufacturer, c.set_name, c.card_number, c.player_or_character, c.team_or_type,
	c.parallel_variant, c.serial_number, c.serial_max, c.is_rookie, c.is_autograph, c.is_memorabilia,
	c.is_graded, c.grading_company, c.grade_value, c.cert_number, c.estimated_raw_condition,
	c.centering_ratio_lr, c.centering_ratio_tb, c.corner_wear_score,
	c.market_price_cents, c.acquisition_cost_cents, c.asking_price_cents, c.player_impact_tier,
	c.scarcity_multiplier, c.hoot_score, c.inventory_status, c.marketplace_meta_json,
	c.front_image_url, c.back_image_url, c.created_at, c.updated_at`

const locJoinCols = `l.id, l.vault_code, l.case_code, l.box_code, l.row_code, l.slot_number, l.capacity, l.occupied_count, l.barcode_label, l.created_at`

func scanRecord(r rowScanner) (domain.InventoryRecord, error) {
	var (
		c                                                      domain.Card
		jobID, locID, team, serial, grader, cert, cond, lr, tb sql.NullString
		backURL                                                sql.NullString
		checklistID, serialMax                                 sql.NullInt64
		grade, wear                                            sql.NullFloat64
		lID, lV, lC, lB, lR, lBar                              sql.NullString
		lSlot, lCap, lOcc                                      sql.NullInt64
		lCreated                                               sql.NullTime
	)
	err := r.Scan(&c.ID, &c.SKU, &jobID, &checklistID, &locID,
		&c.Category, &c.ReleaseYear, &c.Manufacturer, &c.SetName, &c.CardNumber, &c.PlayerOrCharacter, &team,
		&c.ParallelVariant, &serial, &serialMax, &c.IsRookie, &c.IsAutograph, &c.IsMemorabilia,
		&c.IsGraded, &grader, &grade, &cert, &cond, &lr, &tb, &wear,
		&c.MarketPriceCents, &c.AcquisitionCostCents, &c.AskingPriceCents, &c.PlayerImpactTier,
		&c.ScarcityMultiplier, &c.HootScore, &c.InventoryStatus, &c.MarketplaceMetaJSON,
		&c.FrontImageURL, &backURL, &c.CreatedAt, &c.UpdatedAt,
		&lID, &lV, &lC, &lB, &lR, &lSlot, &lCap, &lOcc, &lBar, &lCreated)
	if err != nil {
		return domain.InventoryRecord{}, err
	}
	c.IngestionJobID, c.StorageLocationID, c.TeamOrType = jobID.String, locID.String, team.String
	c.ChecklistID, c.SerialNumber, c.SerialMax = int64Ptr(checklistID), serial.String, intPtr(serialMax)
	c.GradingCompany, c.GradeValue, c.CertNumber = domain.GradingCompany(grader.String), floatPtr(grade), cert.String
	c.EstimatedRawCondition, c.CenteringRatioLR, c.CenteringRatioTB = domain.RawCondition(cond.String), lr.String, tb.String
	c.CornerWearScore, c.BackImageURL = floatPtr(wear), backURL.String
	rec := domain.InventoryRecord{Card: &c}
	if lID.Valid {
		rec.Location = &domain.StorageLocation{ID: lID.String, VaultCode: lV.String, CaseCode: lC.String, BoxCode: lB.String,
			RowCode: lR.String, SlotNumber: int(lSlot.Int64), Capacity: int(lCap.Int64), OccupiedCount: int(lOcc.Int64),
			BarcodeLabel: lBar.String, CreatedAt: lCreated.Time}
	}
	return rec, nil
}

// FinalizeCard allocates storage, assigns the SKU, inserts the card and completes its job atomically.
func (r *InventoryRepository) FinalizeCard(ctx context.Context, c *domain.Card) error {
	if c.ID == "" {
		return fmt.Errorf("sqlite.InventoryRepository.FinalizeCard: %w: card id required", domain.ErrInvalid)
	}
	if err := c.Validate(); err != nil {
		return fmt.Errorf("sqlite.InventoryRepository.FinalizeCard [%s]: %w", c.ID, err)
	}
	orig := *c
	err := r.db.inTx(ctx, func(tx *sql.Tx) error {
		if c.StorageLocationID == "" {
			loc, err := allocateNextTx(ctx, tx)
			if err != nil {
				return fmt.Errorf("allocate storage: %w", err)
			}
			c.StorageLocationID = loc.ID
		} else if _, err := tx.ExecContext(ctx, `UPDATE storage_locations SET occupied_count = occupied_count + 1
			WHERE id = ? AND occupied_count < capacity`, c.StorageLocationID); err != nil {
			return fmt.Errorf("occupy %s: %w", c.StorageLocationID, err)
		}
		if c.SKU == "" {
			sku, err := nextSKUTx(ctx, tx, domain.SKUPrefix(c))
			if err != nil {
				return err
			}
			c.SKU = sku
		}
		if c.MarketplaceMetaJSON == "" {
			c.MarketplaceMetaJSON = "{}"
		}
		if c.ScarcityMultiplier == 0 {
			c.ScarcityMultiplier = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_cards (
			id, sku, ingestion_job_id, checklist_id, storage_location_id,
			category, release_year, manufacturer, set_name, card_number, player_or_character, team_or_type,
			parallel_variant, serial_number, serial_max, is_rookie, is_autograph, is_memorabilia,
			is_graded, grading_company, grade_value, cert_number, estimated_raw_condition,
			centering_ratio_lr, centering_ratio_tb, corner_wear_score,
			market_price_cents, acquisition_cost_cents, asking_price_cents, player_impact_tier,
			scarcity_multiplier, hoot_score, inventory_status, marketplace_meta_json, front_image_url, back_image_url)
			VALUES (?,?,?,?,?, ?,?,?,?,?,?,?, ?,?,?,?,?,?, ?,?,?,?,?, ?,?,?, ?,?,?,?, ?,?,?,?,?,?)`,
			c.ID, c.SKU, nullStr(c.IngestionJobID), nullInt64(c.ChecklistID), nullStr(c.StorageLocationID),
			c.Category, c.ReleaseYear, c.Manufacturer, c.SetName, c.CardNumber, c.PlayerOrCharacter, nullStr(c.TeamOrType),
			c.ParallelVariant, nullStr(c.SerialNumber), nullInt(c.SerialMax), c.IsRookie, c.IsAutograph, c.IsMemorabilia,
			c.IsGraded, nullStr(string(c.GradingCompany)), nullFloat(c.GradeValue), nullStr(c.CertNumber), nullStr(string(c.EstimatedRawCondition)),
			nullStr(c.CenteringRatioLR), nullStr(c.CenteringRatioTB), nullFloat(c.CornerWearScore),
			c.MarketPriceCents, c.AcquisitionCostCents, c.AskingPriceCents, c.PlayerImpactTier,
			c.ScarcityMultiplier, c.HootScore, c.InventoryStatus, c.MarketplaceMetaJSON, c.FrontImageURL, nullStr(c.BackImageURL)); err != nil {
			return fmt.Errorf("insert card: %w", err)
		}
		if c.IngestionJobID != "" {
			res, err := tx.ExecContext(ctx, `UPDATE ingestion_jobs SET status = 'COMPLETED', error_message = NULL, updated_at = CURRENT_TIMESTAMP
				WHERE id = ? AND status IN ('MATCHING', 'NEEDS_REVIEW')`, c.IngestionJobID)
			if err != nil {
				return fmt.Errorf("complete job %s: %w", c.IngestionJobID, err)
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return fmt.Errorf("complete job %s: %w: job is not awaiting finalization", c.IngestionJobID, domain.ErrConflict)
			}
		}
		return nil
	})
	if err != nil {
		*c = orig // do not leak a SKU / slot that was rolled back
		return fmt.Errorf("sqlite.InventoryRepository.FinalizeCard [%s]: %w", c.ID, err)
	}
	return nil
}

// nextSKUTx returns prefix-NNN with NNN one past the highest existing sequence.
func nextSKUTx(ctx context.Context, tx *sql.Tx, prefix string) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT sku FROM inventory_cards WHERE substr(sku, 1, ?) = ?`, len(prefix)+1, prefix+"-")
	if err != nil {
		return "", fmt.Errorf("sku sequence %s: %w", prefix, err)
	}
	defer rows.Close()
	maxSeq := 0
	for rows.Next() {
		var sku string
		if err := rows.Scan(&sku); err != nil {
			return "", fmt.Errorf("sku sequence %s: scan: %w", prefix, err)
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(sku, prefix+"-")); err == nil && n > maxSeq {
			maxSeq = n
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("sku sequence %s: %w", prefix, err)
	}
	return domain.FormatSKU(prefix, maxSeq+1), nil
}

const recordSelect = `SELECT ` + cardCols + `, ` + locJoinCols + ` FROM inventory_cards c
	LEFT JOIN storage_locations l ON l.id = c.storage_location_id`

// Get loads one card with its location.
func (r *InventoryRepository) Get(ctx context.Context, id string) (*domain.InventoryRecord, error) {
	rec, err := scanRecord(r.db.QueryRowContext(ctx, recordSelect+` WHERE c.id = ? OR c.sku = ?`, id, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("sqlite.InventoryRepository.Get [%s]: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite.InventoryRepository.Get [%s]: %w", id, err)
	}
	return &rec, nil
}

// List returns cards matching the filter ordered by Hoot-Score (desc).
func (r *InventoryRepository) List(ctx context.Context, f domain.InventoryFilter) ([]domain.InventoryRecord, error) {
	var where []string
	var args []any
	add := func(clause string, a ...any) { where = append(where, clause); args = append(args, a...) }
	if f.Category != "" {
		add("c.category = ?", f.Category)
	}
	if f.ReleaseYear > 0 {
		add("c.release_year = ?", f.ReleaseYear)
	}
	if f.MinHootScore > 0 {
		add("c.hoot_score >= ?", f.MinHootScore)
	}
	if f.MaxHootScore > 0 {
		add("c.hoot_score <= ?", f.MaxHootScore)
	}
	if f.BoxCode != "" {
		add("l.box_code = ?", f.BoxCode)
	}
	if f.Status != "" {
		add("c.inventory_status = ?", f.Status)
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		like := "%" + strings.ToLower(s) + "%"
		add("(lower(c.sku) LIKE ? OR lower(c.player_or_character) LIKE ? OR lower(c.set_name) LIKE ? OR lower(c.manufacturer) LIKE ?)", like, like, like, like)
	}
	q := recordSelect
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q += " ORDER BY c.hoot_score DESC, c.created_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, max(f.Offset, 0))
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite.InventoryRepository.List: %w", err)
	}
	defer rows.Close()
	var out []domain.InventoryRecord
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite.InventoryRepository.List: scan: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite.InventoryRepository.List: %w", err)
	}
	return out, nil
}

// UpdateValuation persists pricing, Hoot-Score and marketplace metadata.
func (r *InventoryRepository) UpdateValuation(ctx context.Context, c *domain.Card) error {
	res, err := r.db.ExecContext(ctx, `UPDATE inventory_cards SET market_price_cents = ?, asking_price_cents = ?,
		player_impact_tier = ?, scarcity_multiplier = ?, hoot_score = ?, marketplace_meta_json = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, c.MarketPriceCents, c.AskingPriceCents, c.PlayerImpactTier, c.ScarcityMultiplier, c.HootScore, c.MarketplaceMetaJSON, c.ID)
	if err != nil {
		return fmt.Errorf("sqlite.InventoryRepository.UpdateValuation [%s]: %w", c.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("sqlite.InventoryRepository.UpdateValuation [%s]: %w", c.ID, domain.ErrNotFound)
	}
	return nil
}
