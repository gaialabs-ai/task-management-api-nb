package services

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/google/uuid"
)

// CommentService provides task comment management operations (list, add and
// delete) over an in-memory store keyed by task ID.
//
// Access to the comments map is guarded by mu, a sync.RWMutex (read lock for
// listing, write lock for changes), so it is safe for concurrent use. It depends
// on a TaskService to verify that tasks exist. Data is not persisted. Construct it
// with NewCommentService.
type CommentService struct {
	mu          sync.RWMutex
	comments    map[string][]*models.Comment // taskID -> comments
	taskService *TaskService
}

// NewCommentService creates a CommentService with an empty store that uses
// taskService to check that the target task exists. taskService must not be nil,
// or later calls will panic.
func NewCommentService(taskService *TaskService) *CommentService {
	return &CommentService{
		comments:    make(map[string][]*models.Comment),
		taskService: taskService,
	}
}

// ListComments returns all comments for the task with the given taskID, sorted by
// creation time ascending, together with their count in Total.
//
// It returns a "task with ID <id> not found" error if the task does not exist
// (any lookup error is reported that way). A task without comments yields an empty
// list, not an error. The comments are value copies, and the read lock is held
// while copying and sorting.
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

// AddComment adds a comment with the given Author and Body to the task identified
// by taskID and returns it.
//
// The comment gets a new UUID and identical CreatedAt and UpdatedAt set to the
// current time. It returns a "task with ID <id> not found" error if the task does
// not exist. Author and Body are not validated here, so any validation must happen
// before this call. It takes the write lock. The returned pointer is the stored
// comment, not a copy. The task itself is not modified (its UpdatedAt is not
// changed) and no notification is sent.
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

// DeleteComment removes the comment commentID from the task identified by taskID.
//
// It returns a "task with ID <id> not found" error if the task does not exist, and
// a "comment with ID <id> not found on task <id>" error if no such comment is on
// that task (a comment ID belonging to another task is not found). It takes the
// write lock; removal preserves the order of the remaining comments.
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
