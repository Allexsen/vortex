package worker

import "vortex/internal/models"

type taskResult struct {
	Task          models.CrawlTask
	Content       string
	NewCrawlTasks []models.CrawlTask
}
