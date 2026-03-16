package models

import "time"

type CrawlTask struct {
	TraceID    string    `json:"trace_id"`
	URL        string    `json:"url"`
	Attempt    int       `json:"attempt_count"`
	EnqueuedAt time.Time `json:"enqueued_at"`
}
