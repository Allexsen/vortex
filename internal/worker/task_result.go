package worker

import "vortex/internal/models"

type taskResult struct {
	TraceID    string
	TaskURL    string
	CrawlTasks []models.CrawlTask
	Content    string
}
