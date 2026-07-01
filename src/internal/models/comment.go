package models

import (
	"time"
)

// Comment represents a comment/note attached to a task
type Comment struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateCommentRequest represents a request to add a comment to a task
type CreateCommentRequest struct {
	Author string `json:"author" binding:"required,min=1,max=100"`
	Body   string `json:"body" binding:"required,min=1,max=5000"`
}

// ListCommentsResponse represents the response for listing comments on a task
type ListCommentsResponse struct {
	Comments []Comment `json:"comments"`
	Total    int       `json:"total"`
}
