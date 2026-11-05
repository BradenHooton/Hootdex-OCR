package domain

import "time"

// EventType classifies pipeline progress events streamed to the operator UI.
type EventType string

const (
	EventJobQueued    EventType = "job.queued"
	EventJobStatus    EventType = "job.status"
	EventJobCompleted EventType = "job.completed"
	EventJobReview    EventType = "job.needs_review"
	EventJobFailed    EventType = "job.failed"
	EventCardApproved EventType = "card.approved"
	EventQueueStats   EventType = "queue.stats"
)

// PipelineEvent is one real-time progress notification.
type PipelineEvent struct {
	Type       EventType         `json:"type"`
	JobID      string            `json:"job_id,omitempty"`
	BatchID    string            `json:"batch_id,omitempty"`
	Status     JobStatus         `json:"status,omitempty"`
	Message    string            `json:"message,omitempty"`
	Confidence float64           `json:"confidence,omitempty"`
	SKU        string            `json:"sku,omitempty"`
	Worker     int               `json:"worker,omitempty"`
	Queue      map[JobStatus]int `json:"queue,omitempty"`
	Stats      *PipelineStats    `json:"stats,omitempty"`
	At         time.Time         `json:"at"`
}

// PipelineStats summarises worker activity and throughput.
type PipelineStats struct {
	Workers        int     `json:"workers"`
	ActiveWorkers  int     `json:"active_workers"`
	Processed      int64   `json:"processed"`
	Failed         int64   `json:"failed"`
	CardsPerMinute float64 `json:"cards_per_minute"`
}
