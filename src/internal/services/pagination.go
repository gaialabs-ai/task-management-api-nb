package services

import "strconv"

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// paginate computes the page window for a result set of the given total size.
// pageSize <= 0 falls back to the default (20) and values above 100 are
// clamped. An empty, non-numeric or negative pageToken starts at 0; a token
// beyond total yields an empty window positioned at total. next is the token
// for the following page, or "" when there are no more results.
func paginate(total, pageSize int, pageToken string) (start, end int, next string) {
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	if pageToken != "" {
		if idx, err := strconv.Atoi(pageToken); err == nil {
			start = idx
		}
	}
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}

	end = start + pageSize
	if end > total || end < start { // end < start guards integer overflow
		end = total
	}

	if end < total {
		next = strconv.Itoa(end)
	}
	return start, end, next
}
