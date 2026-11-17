package models

import (
	"time"
)

// Comment represents a comment/note attached to a task. ID, TaskID and the
// timestamps are assigned by the service layer; clients supply only Author and
// Body through CreateCommentRequest.
type Comment struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateCommentRequest represents the request body for adding a comment to a
// task. Author is required (1 to 100 characters) and Body is required (1 to 5000
// characters). Violations are rejected at binding time, before the service is
// called.
type CreateCommentRequest struct {
	Author string `json:"author" binding:"required,min=1,max=100"`
	Body   string `json:"body" binding:"required,min=1,max=5000"`
}

// ListCommentsResponse is the response body for listing a task's comments.
// Comments holds the comments (ordered by creation time by the service) and
// Total the number of comments returned.
type ListCommentsResponse struct {
	Comments []Comment `json:"comments"`
	Total    int       `json:"total"`
}
