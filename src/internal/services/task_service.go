package services

import (
	"errors"
	"fmt"
	"log"
	"net/mail"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/google/uuid"
)

// TaskService provides task management operations
type TaskService struct {
	mu       sync.RWMutex
	tasks    map[string]*models.Task
	now      func() time.Time
	notifier Notifier
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

// SetClock replaces the clock used for overdue checks and assignment
// timestamps. A nil clock restores time.Now.
func (s *TaskService) SetClock(clock func() time.Time) {
	if clock == nil {
		clock = time.Now
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = clock
}

// SetNotifier replaces the assignment notifier. A nil notifier restores the
// logging default.
func (s *TaskService) SetNotifier(n Notifier) {
	if n == nil {
		n = LogNotifier{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifier = n
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

	// Apply sorting
	s.sortTasks(tasks, query.SortOrder)

	// Apply pagination
	return buildListResponse(tasks, query.PageSize, query.PageToken), nil
}

// buildListResponse paginates tasks and builds the response. Callers must hold
// the lock that protects the underlying tasks.
func buildListResponse(tasks []*models.Task, pageSize int, pageToken string) *models.ListTasksResponse {
	totalCount := len(tasks)
	start, end, next := paginate(totalCount, pageSize, pageToken)

	var paginatedTasks []models.Task
	for _, task := range tasks[start:end] {
		paginatedTasks = append(paginatedTasks, *task)
	}

	return &models.ListTasksResponse{
		Tasks:         paginatedTasks,
		NextPageToken: next,
		TotalCount:    totalCount,
	}
}

// GetTask retrieves a task by ID
func (s *TaskService) GetTask(id string) (*models.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, exists := s.tasks[id]
	if !exists {
		return nil, fmt.Errorf("task with ID %s not found", id)
	}

	return task, nil
}

// CreateTask creates a new task
func (s *TaskService) CreateTask(req models.CreateTaskRequest) (*models.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
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
		return nil, fmt.Errorf("task with ID %s not found", id)
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
			now := time.Now()
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

	task.UpdatedAt = time.Now()
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
		return fmt.Errorf("task with ID %s not found", id)
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
		sort.Slice(tasks, func(i, j int) bool {
			return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
		})
	case models.SortOrderCreatedAtDesc:
		sort.Slice(tasks, func(i, j int) bool {
			return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
		})
	case models.SortOrderDueDateAsc:
		sort.Slice(tasks, func(i, j int) bool {
			if tasks[i].DueDate == nil {
				return false
			}
			if tasks[j].DueDate == nil {
				return true
			}
			return tasks[i].DueDate.Before(*tasks[j].DueDate)
		})
	case models.SortOrderDueDateDesc:
		sort.Slice(tasks, func(i, j int) bool {
			if tasks[i].DueDate == nil {
				return false
			}
			if tasks[j].DueDate == nil {
				return true
			}
			return tasks[i].DueDate.After(*tasks[j].DueDate)
		})
	case models.SortOrderPriorityAsc:
		sort.Slice(tasks, func(i, j int) bool {
			return tasks[i].Priority.GetValue() < tasks[j].Priority.GetValue()
		})
	case models.SortOrderPriorityDesc:
		sort.Slice(tasks, func(i, j int) bool {
			return tasks[i].Priority.GetValue() > tasks[j].Priority.GetValue()
		})
	}
}

// ListOverdueTasks returns tasks whose due date is before the service clock's
// current time and whose status is neither completed nor cancelled, sorted by
// due date ascending (ties broken by CreatedAt, then ID). The assigned_to and
// tags filters of the query apply; status and sort_order are ignored.
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

	query.Status = ""
	overdue = s.filterTasks(overdue, query)

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

	return buildListResponse(overdue, query.PageSize, query.PageToken), nil
}

// AssignTask assigns a task to the given bare email address and notifies the
// notifier after the lock has been released. Notifier failures are logged and
// do not fail the assignment. It returns a copy of the updated task.
func (s *TaskService) AssignTask(id, email string) (*models.Task, error) {
	trimmed := strings.TrimSpace(email)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: assignee must not be empty", ErrInvalidEmail)
	}
	parsed, err := mail.ParseAddress(trimmed)
	if err != nil || parsed.Address != trimmed {
		return nil, fmt.Errorf("%w: %q", ErrInvalidEmail, trimmed)
	}

	s.mu.Lock()
	task, exists := s.tasks[id]
	if !exists {
		s.mu.Unlock()
		return nil, &TaskNotFoundError{ID: id}
	}
	assignee := trimmed
	task.AssignedTo = &assignee
	task.UpdatedAt = s.now()
	snapshot := *task
	notifier := s.notifier
	s.mu.Unlock()

	if nerr := notifier.Notify(snapshot, trimmed); nerr != nil {
		log.Printf("failed to send assignment notification for task %s: %v", id, nerr)
	}
	return &snapshot, nil
}

// IsNotFound reports whether err is a task-not-found error.
func IsNotFound(err error) bool {
	var nf *TaskNotFoundError
	return errors.As(err, &nf)
}
