package services

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/google/uuid"
)

// CommentService provides task comment management operations
type CommentService struct {
	mu          sync.RWMutex
	comments    map[string][]*models.Comment // taskID -> comments
	taskService *TaskService
}

// NewCommentService creates a new comment service instance
func NewCommentService(taskService *TaskService) *CommentService {
	return &CommentService{
		comments:    make(map[string][]*models.Comment),
		taskService: taskService,
	}
}

// ListComments returns all comments for a task, sorted by creation time
func (s *CommentService) ListComments(taskID string) (*models.ListCommentsResponse, error) {
	if _, err := s.taskService.GetTask(taskID); err != nil {
		return nil, fmt.Errorf("task with ID %s not found", taskID)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	comments := s.comments[taskID]
	result := make([]models.Comment, 0, len(comments))
	for _, c := range comments {
		result = append(result, *c)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})

	return &models.ListCommentsResponse{
		Comments: result,
		Total:    len(result),
	}, nil
}

// AddComment adds a comment to a task
func (s *CommentService) AddComment(taskID string, req models.CreateCommentRequest) (*models.Comment, error) {
	if _, err := s.taskService.GetTask(taskID); err != nil {
		return nil, fmt.Errorf("task with ID %s not found", taskID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	comment := &models.Comment{
		ID:        uuid.New().String(),
		TaskID:    taskID,
		Author:    req.Author,
		Body:      req.Body,
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.comments[taskID] = append(s.comments[taskID], comment)
	return comment, nil
}

// DeleteComment removes a comment from a task
func (s *CommentService) DeleteComment(taskID, commentID string) error {
	if _, err := s.taskService.GetTask(taskID); err != nil {
		return fmt.Errorf("task with ID %s not found", taskID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	comments := s.comments[taskID]
	for i, c := range comments {
		if c.ID == commentID {
			s.comments[taskID] = append(comments[:i], comments[i+1:]...)
			return nil
		}
	}

	return fmt.Errorf("comment with ID %s not found on task %s", commentID, taskID)
}
