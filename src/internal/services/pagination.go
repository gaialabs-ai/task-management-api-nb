package services

import (
	"strconv"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// paginateTasks applies the shared pagination rules to an already filtered and
// sorted slice: page size defaults to 20 and is clamped to 1-100, the page
// token is a numeric offset (non-numeric tokens mean offset 0, negative
// offsets are treated as 0), and next_page_token is the offset of the next
// page when more results remain. total_count is the length of tasks.
// Tasks are copied by value so callers can release locks afterwards.
func paginateTasks(tasks []*models.Task, pageSize int, pageToken string) *models.ListTasksResponse {
	totalCount := len(tasks)
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	startIndex := 0
	if pageToken != "" {
		if idx, err := strconv.Atoi(pageToken); err == nil && idx > 0 {
			startIndex = idx
		}
	}

	endIndex := startIndex + pageSize
	if endIndex > totalCount {
		endIndex = totalCount
	}

	var page []models.Task
	if startIndex < totalCount {
		for _, task := range tasks[startIndex:endIndex] {
			page = append(page, *task)
		}
	}

	var nextPageToken string
	if endIndex < totalCount {
		nextPageToken = strconv.Itoa(endIndex)
	}

	return &models.ListTasksResponse{
		Tasks:         page,
		NextPageToken: nextPageToken,
		TotalCount:    totalCount,
	}
}
