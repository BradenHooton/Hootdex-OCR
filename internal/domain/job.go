package domain

import (
	"fmt"
	"time"
)

// JobStatus is the ingestion_jobs.status state machine.
type JobStatus string

const (
	JobPending       JobStatus = "PENDING"
	JobPreprocessing JobStatus = "PREPROCESSING"
	JobExtracting    JobStatus = "EXTRACTING"
	JobMatching      JobStatus = "MATCHING"
	JobNeedsReview   JobStatus = "NEEDS_REVIEW"
	JobCompleted     JobStatus = "COMPLETED"
	JobFailed        JobStatus = "FAILED"
)

// Valid reports whether the status is allowed by the schema.
func (s JobStatus) Valid() bool {
	switch s {
	case JobPending, JobPreprocessing, JobExtracting, JobMatching, JobNeedsReview, JobCompleted, JobFailed:
		return true
	}
	return false
}

// IsTerminal reports whether the job is finished (no worker will touch it again).
func (s JobStatus) IsTerminal() bool { return s == JobCompleted || s == JobFailed }

// IsInFlight reports whether a worker was actively processing the job.
// In-flight jobs found at startup were interrupted by a crash and must be requeued.
func (s JobStatus) IsInFlight() bool {
	return s == JobPreprocessing || s == JobExtracting || s == JobMatching
}

var jobTransitions = map[JobStatus][]JobStatus{
	JobPending:       {JobPreprocessing, JobFailed},
	JobPreprocessing: {JobExtracting, JobFailed, JobPending},
	JobExtracting:    {JobMatching, JobFailed, JobPending},
	JobMatching:      {JobCompleted, JobNeedsReview, JobFailed, JobPending},
	JobNeedsReview:   {JobCompleted, JobFailed},
	JobFailed:        {JobPending},
}

// CanTransition reports whether from -> to is a legal state change.
func CanTransition(from, to JobStatus) bool {
	for _, s := range jobTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// ExtractorKind identifies which OCR/VLM engine produced a payload.
type ExtractorKind string

const (
	ExtractorTesseract ExtractorKind = "TESSERACT_LOCAL"
	ExtractorOllama    ExtractorKind = "OLLAMA_VLM"
	ExtractorClaude    ExtractorKind = "CLAUDE_API"
)

// MaxJobRetries is how many times a failed / interrupted job is retried before FAILED sticks.
const MaxJobRetries = 3

// IngestionJob is one durable queue entry (ingestion_jobs row).
type IngestionJob struct {
	ID                   string        `json:"id"`
	BatchID              string        `json:"batch_id"`
	FrontImagePath       string        `json:"front_image_path"`
	BackImagePath        string        `json:"back_image_path,omitempty"`
	CroppedFrontPath     string        `json:"cropped_front_path,omitempty"`
	CroppedBackPath      string        `json:"cropped_back_path,omitempty"`
	Status               JobStatus     `json:"status"`
	ExtractorUsed        ExtractorKind `json:"extractor_used,omitempty"`
	RawOCRFront          string        `json:"raw_ocr_front,omitempty"`
	RawOCRBack           string        `json:"raw_ocr_back,omitempty"`
	ExtractedPayloadJSON string        `json:"extracted_payload_json,omitempty"`
	PreprocessMetaJSON   string        `json:"preprocess_meta_json,omitempty"`
	ConfidenceScore      float64       `json:"confidence_score"`
	ErrorMessage         string        `json:"error_message,omitempty"`
	RetryCount           int           `json:"retry_count"`
	CreatedAt            time.Time     `json:"created_at"`
	UpdatedAt            time.Time     `json:"updated_at"`
}

// NewIngestionJob creates a PENDING job for a scanned pair.
func NewIngestionJob(id, batchID, frontPath, backPath string) (*IngestionJob, error) {
	if id == "" || batchID == "" || frontPath == "" {
		return nil, fmt.Errorf("%w: job id, batch id and front image are required", ErrInvalid)
	}
	return &IngestionJob{ID: id, BatchID: batchID, FrontImagePath: frontPath, BackImagePath: backPath, Status: JobPending}, nil
}

// TransitionTo moves the job to a new status, enforcing the state machine.
func (j *IngestionJob) TransitionTo(to JobStatus) error {
	if !CanTransition(j.Status, to) {
		return fmt.Errorf("%w: job %s cannot move %s -> %s", ErrConflict, j.ID, j.Status, to)
	}
	j.Status = to
	return nil
}
