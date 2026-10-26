package services

import (
	"strconv"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// paginateTasks applies offset-based pagination to an already filtered and
// sorted slice. The page token is the decimal start offset; an empty or
// unparsable token starts at the beginning. It returns the page (copies of the
// tasks), the token for the next page ("" when there is none) and the total
// number of tasks before pagination.
func paginateTasks(tasks []*models.Task, pageSize int, pageToken string) ([]models.Task, string, int) {
	totalCount := len(tasks)
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	startIndex := 0
	if pageToken != "" {
		if idx, err := strconv.Atoi(pageToken); err == nil && idx >= 0 {
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

	return page, nextPageToken, totalCount
}
