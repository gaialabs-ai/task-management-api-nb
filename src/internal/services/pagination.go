package services

import (
	"strconv"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// paginate applies offset-based pagination to an already filtered and sorted
// slice of tasks. The page token is the decimal start index of the page; an
// empty or unparseable token starts at the beginning. A page size <= 0
// defaults to 20 and is capped at 100.
//
// It returns the page (copies of the tasks, nil when the page is empty), the
// token of the next page (empty when there are no more results) and the total
// number of tasks before pagination.
func paginate(sorted []*models.Task, pageSize int, pageToken string) ([]models.Task, string, int) {
	totalCount := len(sorted)

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
		for _, task := range sorted[startIndex:endIndex] {
			page = append(page, *task)
		}
	}

	var nextPageToken string
	if endIndex < totalCount {
		nextPageToken = strconv.Itoa(endIndex)
	}

	return page, nextPageToken, totalCount
}
