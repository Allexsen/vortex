package models

import "time"

type CrawlResult struct {
	TraceID   string    `json:"trace_id"`
	URL       string    `json:"url"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}
