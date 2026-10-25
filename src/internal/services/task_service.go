package services

import (
	"errors"
	"fmt"
	"log"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/google/uuid"
)

// TaskService provides task management operations
type TaskService struct {
	mu    sync.RWMutex
	tasks map[string]*models.Task

	now      func() time.Time
	notifier Notifier
}

var (
	// ErrTaskNotFound is matched (errors.Is) by the "task not found" error returned from GetTask, UpdateTask, UpdateTaskStatus, AssignTask and DeleteTask.
	ErrTaskNotFound = errors.New("task not found")
	// ErrInvalidEmail is matched (errors.Is) by invalid assignee errors.
	ErrInvalidEmail = errors.New("invalid email")
)

type taskNotFoundError struct{ id string }

func (e *taskNotFoundError) Error() string        { return fmt.Sprintf("task with ID %s not found", e.id) }
func (e *taskNotFoundError) Is(target error) bool { return target == ErrTaskNotFound }

// Notifier is told about task assignments.
type Notifier interface {
	NotifyAssignment(task models.Task, assignee string) error
}

// LogNotifier is the default Notifier; it writes to the standard logger.
type LogNotifier struct{}

// NotifyAssignment logs the assignment.
func (LogNotifier) NotifyAssignment(task models.Task, assignee string) error {
	log.Printf("notification: task %s (%q) assigned to %s", task.ID, task.Title, assignee)
	return nil
}

// SetClock replaces the time source (nil restores time.Now).
func (s *TaskService) SetClock(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	s.mu.Lock()
	s.now = now
	s.mu.Unlock()
}

// SetNotifier replaces the assignment notifier (nil disables notifications).
func (s *TaskService) SetNotifier(n Notifier) {
	s.mu.Lock()
	s.notifier = n
	s.mu.Unlock()
}

// NewTaskService creates a new task service instance
func NewTaskService() *TaskService {
	s := &TaskService{
		tasks:    make(map[string]*models.Task),
		now:      time.Now,
		notifier: LogNotifier{},
	}
	s.initializeSampleData()
	return s
}

// initializeSampleData creates some sample tasks
func (s *TaskService) initializeSampleData() {
	sampleTasks := []models.CreateTaskRequest{
		{
			Title:       "Implement Go REST API",
			Description: "Create REST API with Gin framework",
			Priority:    models.TaskPriorityHigh,
			Tags:        []string{"go", "rest", "api"},
		},
		{
			Title:       "Add gRPC support",
			Description: "Implement gRPC server with native Go support",
			Priority:    models.TaskPriorityMedium,
			Tags:        []string{"go", "grpc", "protobuf"},
		},
	}

	for _, req := range sampleTasks {
		s.CreateTask(req)
	}
}

// ListTasks returns a paginated list of tasks with optional filters
func (s *TaskService) ListTasks(query models.ListTasksQuery) (*models.ListTasksResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Convert map to slice for filtering and sorting
	var tasks []*models.Task
	for _, task := range s.tasks {
		tasks = append(tasks, task)
	}

	// Apply filters
	tasks = s.filterTasks(tasks, query)

	// Establish a deterministic base order (map iteration is random) so that
	// pagination is stable; the requested sort order is applied stably on top.
	// Intentional change vs. the original: the default order used to be random map order.
	sort.Slice(tasks, func(i, j int) bool {
		if !tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
			return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
		}
		return tasks[i].ID < tasks[j].ID
	})

	// Apply sorting
	s.sortTasks(tasks, query.SortOrder)

	// Apply pagination
	totalCount := len(tasks)
	paginatedTasks, nextPageToken := paginateTasks(tasks, query.PageSize, query.PageToken)

	return &models.ListTasksResponse{
		Tasks:         paginatedTasks,
		NextPageToken: nextPageToken,
		TotalCount:    totalCount,
	}, nil
}

// paginateTasks applies page_size/page_token offset pagination to an already
// filtered and sorted slice. Page size defaults to 20 and is capped at 100; the
// token is a decimal offset (invalid or negative tokens are treated as 0; the
// original code panicked on negative offsets). It returns copies
// of the tasks on the page (nil when the page is empty) and the next page token
// ("" when there are no more results).
func paginateTasks(tasks []*models.Task, pageSize int, pageToken string) ([]models.Task, string) {
	totalCount := len(tasks)
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
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

	var next string
	if endIndex < totalCount {
		next = strconv.Itoa(endIndex)
	}
	return page, next
}

// ListOverdueTasks returns tasks whose due date is before the current time and
// whose status is neither completed nor cancelled, ordered by due date ascending
// (ties broken by creation time, then ID), paginated like ListTasks. The Tasks
// slice is always non-nil.
func (s *TaskService) ListOverdueTasks(query models.ListTasksQuery) (*models.ListTasksResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.now()
	var overdue []*models.Task
	for _, task := range s.tasks {
		if task.DueDate == nil || !task.DueDate.Before(now) {
			continue
		}
		if task.Status == models.TaskStatusCompleted || task.Status == models.TaskStatusCancelled {
			continue
		}
		overdue = append(overdue, task)
	}

	sort.Slice(overdue, func(i, j int) bool {
		a, b := overdue[i], overdue[j]
		if !a.DueDate.Equal(*b.DueDate) {
			return a.DueDate.Before(*b.DueDate)
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})

	page, next := paginateTasks(overdue, query.PageSize, query.PageToken)
	copies := make([]models.Task, len(page))
	for i := range page {
		copies[i] = cloneTask(page[i])
	}

	return &models.ListTasksResponse{
		Tasks:         copies,
		NextPageToken: next,
		TotalCount:    len(overdue),
	}, nil
}

// AssignTask assigns a task to the given bare email address. It returns an error
// matching ErrInvalidEmail or ErrTaskNotFound (via errors.Is) on failure. The
// notifier is invoked after the lock is released; its errors are logged only.
func (s *TaskService) AssignTask(id, email string) (*models.Task, error) {
	trimmed := strings.TrimSpace(email)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: assignee is required", ErrInvalidEmail)
	}
	parsed, err := mail.ParseAddress(trimmed)
	if err != nil || parsed.Address != trimmed {
		return nil, fmt.Errorf("%w: %q", ErrInvalidEmail, email)
	}

	s.mu.Lock()
	task, exists := s.tasks[id]
	if !exists {
		s.mu.Unlock()
		return nil, &taskNotFoundError{id: id}
	}
	assignee := trimmed
	task.AssignedTo = &assignee
	task.UpdatedAt = s.now()
	result := cloneTask(*task)
	notifier := s.notifier
	s.mu.Unlock()

	if notifier != nil {
		if nerr := notifier.NotifyAssignment(cloneTask(result), trimmed); nerr != nil {
			log.Printf("failed to notify assignment of task %s to %s: %v", id, trimmed, nerr)
		}
	}
	return &result, nil
}

// cloneTask returns a copy of t that shares no mutable memory with it.
func cloneTask(t models.Task) models.Task {
	if t.Tags != nil {
		t.Tags = append([]string(nil), t.Tags...)
	}
	if t.AssignedTo != nil {
		v := *t.AssignedTo
		t.AssignedTo = &v
	}
	if t.DueDate != nil {
		v := *t.DueDate
		t.DueDate = &v
	}
	if t.CompletedAt != nil {
		v := *t.CompletedAt
		t.CompletedAt = &v
	}
	return t
}

// GetTask retrieves a task by ID
func (s *TaskService) GetTask(id string) (*models.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, exists := s.tasks[id]
	if !exists {
		return nil, &taskNotFoundError{id: id}
	}

	return task, nil
}

// CreateTask creates a new task
func (s *TaskService) CreateTask(req models.CreateTaskRequest) (*models.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	task := &models.Task{
		ID:          uuid.New().String(),
		Title:       req.Title,
		Description: req.Description,
		Status:      models.TaskStatusPending,
		Priority:    req.Priority,
		Tags:        req.Tags,
		CreatedBy:   "system",
		AssignedTo:  req.AssignedTo,
		CreatedAt:   now,
		UpdatedAt:   now,
		DueDate:     req.DueDate,
	}

	// Set default priority if not specified
	if task.Priority == "" {
		task.Priority = models.TaskPriorityMedium
	}

	// Validate priority
	if !task.Priority.IsValid() {
		return nil, fmt.Errorf("invalid priority: %s", task.Priority)
	}

	// Initialize empty tags slice if nil
	if task.Tags == nil {
		task.Tags = []string{}
	}

	s.tasks[task.ID] = task
	return task, nil
}

// UpdateTask updates an existing task
func (s *TaskService) UpdateTask(id string, req models.UpdateTaskRequest) (*models.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, exists := s.tasks[id]
	if !exists {
		return nil, &taskNotFoundError{id: id}
	}

	// Update fields if provided
	if req.Title != nil {
		task.Title = *req.Title
	}
	if req.Description != nil {
		task.Description = *req.Description
	}
	if req.Status != nil {
		if !req.Status.IsValid() {
			return nil, fmt.Errorf("invalid status: %s", *req.Status)
		}
		task.Status = *req.Status

		// Set completed time if status changes to completed
		if task.Status == models.TaskStatusCompleted && task.CompletedAt == nil {
			now := s.now()
			task.CompletedAt = &now
		}
	}
	if req.Priority != nil {
		if !req.Priority.IsValid() {
			return nil, fmt.Errorf("invalid priority: %s", *req.Priority)
		}
		task.Priority = *req.Priority
	}
	if req.Tags != nil {
		task.Tags = req.Tags
	}
	if req.AssignedTo != nil {
		task.AssignedTo = req.AssignedTo
	}
	if req.DueDate != nil {
		task.DueDate = req.DueDate
	}

	task.UpdatedAt = s.now()
	return task, nil
}

// UpdateTaskStatus updates the status of a task
func (s *TaskService) UpdateTaskStatus(id string, status models.TaskStatus) (*models.Task, error) {
	return s.UpdateTask(id, models.UpdateTaskRequest{
		Status: &status,
	})
}

// GetStats returns aggregate counts of tasks grouped by status and priority.
// All known status and priority keys are present in the response (zero-initialized
// when empty) so consumers can rely on a stable shape.
func (s *TaskService) GetStats() *models.TaskStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := &models.TaskStats{
		Total: len(s.tasks),
		ByStatus: map[models.TaskStatus]int{
			models.TaskStatusPending:    0,
			models.TaskStatusInProgress: 0,
			models.TaskStatusCompleted:  0,
			models.TaskStatusCancelled:  0,
			models.TaskStatusOnHold:     0,
		},
		ByPriority: map[models.TaskPriority]int{
			models.TaskPriorityLow:      0,
			models.TaskPriorityMedium:   0,
			models.TaskPriorityHigh:     0,
			models.TaskPriorityCritical: 0,
		},
	}

	for _, task := range s.tasks {
		stats.ByStatus[task.Status]++
		stats.ByPriority[task.Priority]++
	}

	return stats
}

// SearchTasks performs a case-insensitive full-text search across task titles,
// descriptions, and tags. Returns matching tasks ordered by relevance (title
// matches first, then description, then tags).
func (s *TaskService) SearchTasks(query models.SearchTasksQuery) *models.SearchTasksResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	q := strings.ToLower(query.Query)
	type scored struct {
		task  *models.Task
		score int
	}

	var matches []scored
	for _, task := range s.tasks {
		score := 0
		if strings.Contains(strings.ToLower(task.Title), q) {
			score += 3
		}
		if strings.Contains(strings.ToLower(task.Description), q) {
			score += 2
		}
		for _, tag := range task.Tags {
			if strings.Contains(strings.ToLower(tag), q) {
				score += 1
				break
			}
		}
		if score > 0 {
			matches = append(matches, scored{task: task, score: score})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})

	limit := query.PageSize
	if limit <= 0 {
		limit = 20
	}
	if limit > len(matches) {
		limit = len(matches)
	}

	tasks := make([]models.Task, 0, limit)
	for _, m := range matches[:limit] {
		tasks = append(tasks, *m.task)
	}

	return &models.SearchTasksResponse{
		Tasks:      tasks,
		TotalCount: len(matches),
		Query:      query.Query,
	}
}

// DeleteTask deletes a task by ID
func (s *TaskService) DeleteTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.tasks[id]; !exists {
		return &taskNotFoundError{id: id}
	}

	delete(s.tasks, id)
	return nil
}

// filterTasks applies filters to the task list
func (s *TaskService) filterTasks(tasks []*models.Task, query models.ListTasksQuery) []*models.Task {
	var filtered []*models.Task

	for _, task := range tasks {
		// Filter by status
		if query.Status != "" && task.Status != query.Status {
			continue
		}

		// Filter by assigned user
		if query.AssignedTo != "" {
			if task.AssignedTo == nil || *task.AssignedTo != query.AssignedTo {
				continue
			}
		}

		// Filter by tags
		if query.Tags != "" {
			tags := strings.Split(query.Tags, ",")
			hasAllTags := true
			for _, tag := range tags {
				tag = strings.TrimSpace(tag)
				found := false
				for _, taskTag := range task.Tags {
					if taskTag == tag {
						found = true
						break
					}
				}
				if !found {
					hasAllTags = false
					break
				}
			}
			if !hasAllTags {
				continue
			}
		}

		filtered = append(filtered, task)
	}

	return filtered
}

// sortTasks sorts tasks based on the specified order
func (s *TaskService) sortTasks(tasks []*models.Task, order models.SortOrder) {
	switch order {
	case models.SortOrderCreatedAtAsc:
		sort.SliceStable(tasks, func(i, j int) bool {
			return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
		})
	case models.SortOrderCreatedAtDesc:
		sort.SliceStable(tasks, func(i, j int) bool {
			return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
		})
	case models.SortOrderDueDateAsc:
		sort.SliceStable(tasks, func(i, j int) bool {
			if tasks[i].DueDate == nil {
				return false
			}
			if tasks[j].DueDate == nil {
				return true
			}
			return tasks[i].DueDate.Before(*tasks[j].DueDate)
		})
	case models.SortOrderDueDateDesc:
		sort.SliceStable(tasks, func(i, j int) bool {
			if tasks[i].DueDate == nil {
				return false
			}
			if tasks[j].DueDate == nil {
				return true
			}
			return tasks[i].DueDate.After(*tasks[j].DueDate)
		})
	case models.SortOrderPriorityAsc:
		sort.SliceStable(tasks, func(i, j int) bool {
			return tasks[i].Priority.GetValue() < tasks[j].Priority.GetValue()
		})
	case models.SortOrderPriorityDesc:
		sort.SliceStable(tasks, func(i, j int) bool {
			return tasks[i].Priority.GetValue() > tasks[j].Priority.GetValue()
		})
	}
}
