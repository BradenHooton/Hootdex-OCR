package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// JobRepository implements ports.JobRepository over ingestion_jobs.
type JobRepository struct{ db *DB }

var _ ports.JobRepository = (*JobRepository)(nil)

// NewJobRepository constructs the repository.
func NewJobRepository(db *DB) *JobRepository { return &JobRepository{db: db} }

const jobCols = `id, batch_id, front_image_path, back_image_path, cropped_front_path, cropped_back_path, status,
	extractor_used, raw_ocr_front, raw_ocr_back, extracted_payload_json, preprocess_meta_json, confidence_score,
	error_message, retry_count, created_at, updated_at`

func scanJob(r rowScanner) (*domain.IngestionJob, error) {
	var (
		j                                                domain.IngestionJob
		back, cf, cb, ext, rf, rb, payload, meta, errMsg sql.NullString
	)
	if err := r.Scan(&j.ID, &j.BatchID, &j.FrontImagePath, &back, &cf, &cb, &j.Status, &ext, &rf, &rb, &payload, &meta,
		&j.ConfidenceScore, &errMsg, &j.RetryCount, &j.CreatedAt, &j.UpdatedAt); err != nil {
		return nil, err
	}
	j.BackImagePath, j.CroppedFrontPath, j.CroppedBackPath = back.String, cf.String, cb.String
	j.ExtractorUsed = domain.ExtractorKind(ext.String)
	j.RawOCRFront, j.RawOCRBack, j.ExtractedPayloadJSON = rf.String, rb.String, payload.String
	j.PreprocessMetaJSON, j.ErrorMessage = meta.String, errMsg.String
	return &j, nil
}

// Create inserts a new job.
func (r *JobRepository) Create(ctx context.Context, j *domain.IngestionJob) error {
	if !j.Status.Valid() {
		return fmt.Errorf("sqlite.JobRepository.Create [%s]: %w: status %q", j.ID, domain.ErrInvalid, j.Status)
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO ingestion_jobs (id, batch_id, front_image_path, back_image_path, status)
		VALUES (?, ?, ?, ?, ?)`, j.ID, j.BatchID, j.FrontImagePath, nullStr(j.BackImagePath), j.Status)
	if err != nil {
		return fmt.Errorf("sqlite.JobRepository.Create [%s]: %w", j.ID, err)
	}
	return nil
}

// Get loads a job by ID.
func (r *JobRepository) Get(ctx context.Context, id string) (*domain.IngestionJob, error) {
	j, err := scanJob(r.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM ingestion_jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("sqlite.JobRepository.Get [%s]: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite.JobRepository.Get [%s]: %w", id, err)
	}
	return j, nil
}

// ClaimNextPending atomically claims the oldest PENDING job with a single UPDATE ... RETURNING.
func (r *JobRepository) ClaimNextPending(ctx context.Context) (*domain.IngestionJob, error) {
	j, err := scanJob(r.db.QueryRowContext(ctx, `UPDATE ingestion_jobs
		SET status = 'PREPROCESSING', updated_at = CURRENT_TIMESTAMP
		WHERE id = (SELECT id FROM ingestion_jobs WHERE status = 'PENDING' ORDER BY created_at, rowid LIMIT 1)
		  AND status = 'PENDING'
		RETURNING `+jobCols))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite.JobRepository.ClaimNextPending: %w", err)
	}
	return j, nil
}

// Update persists the mutable job columns.
func (r *JobRepository) Update(ctx context.Context, j *domain.IngestionJob) error {
	if !j.Status.Valid() {
		return fmt.Errorf("sqlite.JobRepository.Update [%s]: %w: status %q", j.ID, domain.ErrInvalid, j.Status)
	}
	res, err := r.db.ExecContext(ctx, `UPDATE ingestion_jobs SET
		back_image_path = ?, cropped_front_path = ?, cropped_back_path = ?, status = ?, extractor_used = ?,
		raw_ocr_front = ?, raw_ocr_back = ?, extracted_payload_json = ?, preprocess_meta_json = ?,
		confidence_score = ?, error_message = ?, retry_count = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		nullStr(j.BackImagePath), nullStr(j.CroppedFrontPath), nullStr(j.CroppedBackPath), j.Status, nullStr(string(j.ExtractorUsed)),
		nullStr(j.RawOCRFront), nullStr(j.RawOCRBack), nullStr(j.ExtractedPayloadJSON), nullStr(j.PreprocessMetaJSON),
		j.ConfidenceScore, nullStr(j.ErrorMessage), j.RetryCount, j.ID)
	if err != nil {
		return fmt.Errorf("sqlite.JobRepository.Update [%s]: %w", j.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("sqlite.JobRepository.Update [%s]: %w", j.ID, domain.ErrNotFound)
	}
	return nil
}

// ListByStatus returns jobs in a status, oldest first.
func (r *JobRepository) ListByStatus(ctx context.Context, status domain.JobStatus, limit int) ([]*domain.IngestionJob, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+jobCols+` FROM ingestion_jobs WHERE status = ? ORDER BY created_at, rowid LIMIT ?`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite.JobRepository.ListByStatus [%s]: %w", status, err)
	}
	defer rows.Close()
	var out []*domain.IngestionJob
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite.JobRepository.ListByStatus [%s]: scan: %w", status, err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite.JobRepository.ListByStatus [%s]: %w", status, err)
	}
	return out, nil
}

// CountByStatus returns queue depth per status.
func (r *JobRepository) CountByStatus(ctx context.Context) (map[domain.JobStatus]int, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM ingestion_jobs GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("sqlite.JobRepository.CountByStatus: %w", err)
	}
	defer rows.Close()
	out := map[domain.JobStatus]int{}
	for rows.Next() {
		var s domain.JobStatus
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, fmt.Errorf("sqlite.JobRepository.CountByStatus: scan: %w", err)
		}
		out[s] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite.JobRepository.CountByStatus: %w", err)
	}
	return out, nil
}

// RequeueInFlight resets interrupted jobs to PENDING (incrementing retry_count), and
// fails jobs that already exhausted their retries.
func (r *JobRepository) RequeueInFlight(ctx context.Context) (int, error) {
	var n int64
	err := r.db.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE ingestion_jobs
			SET status = 'FAILED', error_message = 'interrupted too many times', updated_at = CURRENT_TIMESTAMP
			WHERE status IN ('PREPROCESSING','EXTRACTING','MATCHING') AND retry_count >= ?`, domain.MaxJobRetries); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE ingestion_jobs
			SET status = 'PENDING', retry_count = retry_count + 1, updated_at = CURRENT_TIMESTAMP
			WHERE status IN ('PREPROCESSING','EXTRACTING','MATCHING')`)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("sqlite.JobRepository.RequeueInFlight: %w", err)
	}
	return int(n), nil
}
