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
	mu       sync.RWMutex
	tasks    map[string]*models.Task
	now      func() time.Time
	notifier Notifier
}

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// Sentinel errors returned by the service; use errors.Is to match them.
var (
	// ErrTaskNotFound is matched (via errors.Is) by any not-found error.
	ErrTaskNotFound = errors.New("task not found")
	// ErrInvalidEmail is returned when an assignee is not a valid bare email address.
	ErrInvalidEmail = errors.New("invalid email address")
)

// TaskNotFoundError is the typed not-found error. Its message matches the
// historical "task with ID %s not found" text.
type TaskNotFoundError struct{ ID string }

func (e *TaskNotFoundError) Error() string { return fmt.Sprintf("task with ID %s not found", e.ID) }

// Is makes errors.Is(err, ErrTaskNotFound) succeed.
func (e *TaskNotFoundError) Is(target error) bool { return target == ErrTaskNotFound }

// Notifier is told when a task is assigned to someone.
type Notifier interface {
	Notify(task models.Task, assignee string) error
}

// LogNotifier is the default Notifier; it only writes a log line.
type LogNotifier struct{}

// Notify logs the assignment.
func (LogNotifier) Notify(task models.Task, assignee string) error {
	log.Printf("notification: task %s (%q) assigned to %s", task.ID, task.Title, assignee)
	return nil
}

// SetClock injects the time source used for overdue evaluation (nil restores time.Now).
func (s *TaskService) SetClock(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if clock == nil {
		clock = time.Now
	}
	s.now = clock
}

// SetNotifier injects the assignment notifier (nil restores the logging default).
func (s *TaskService) SetNotifier(n Notifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n == nil {
		n = LogNotifier{}
	}
	s.notifier = n
}

// paginate applies the shared pagination rules to an already filtered and
// sorted slice: page size defaults to 20 and is capped at 100, the page token
// is the numeric start index, and the next token is the index after the page.
func paginate(tasks []*models.Task, pageSize int, pageToken string) (page []models.Task, nextToken string, total int) {
	total = len(tasks)
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	start := 0
	if pageToken != "" {
		if idx, err := strconv.Atoi(pageToken); err == nil && idx > 0 {
			start = idx
		}
	}

	end := start + pageSize
	if end > total {
		end = total
	}

	if start < total {
		for _, t := range tasks[start:end] {
			page = append(page, *t)
		}
	}
	if end < total {
		nextToken = strconv.Itoa(end)
	}
	return page, nextToken, total
}

// ListOverdueTasks returns tasks whose due date is before the clock's now and
// whose status is neither completed nor cancelled, ordered by due date
// ascending (ties broken by task ID).
func (s *TaskService) ListOverdueTasks(query models.ListOverdueTasksQuery) (*models.ListTasksResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.now()
	var overdue []*models.Task
	for _, t := range s.tasks {
		if t.DueDate == nil || !t.DueDate.Before(now) {
			continue
		}
		if t.Status == models.TaskStatusCompleted || t.Status == models.TaskStatusCancelled {
			continue
		}
		overdue = append(overdue, t)
	}

	sort.Slice(overdue, func(i, j int) bool {
		if !overdue[i].DueDate.Equal(*overdue[j].DueDate) {
			return overdue[i].DueDate.Before(*overdue[j].DueDate)
		}
		return overdue[i].ID < overdue[j].ID
	})

	page, next, total := paginate(overdue, query.PageSize, query.PageToken)
	return &models.ListTasksResponse{Tasks: page, NextPageToken: next, TotalCount: total}, nil
}

// AssignTask validates the email, assigns the task and notifies the assignee.
// Notification failures are logged and never fail the assignment.
func (s *TaskService) AssignTask(id, email string) (*models.Task, error) {
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return nil, fmt.Errorf("%w: %q", ErrInvalidEmail, email)
	}

	s.mu.Lock()
	task, exists := s.tasks[id]
	if !exists {
		s.mu.Unlock()
		return nil, &TaskNotFoundError{ID: id}
	}
	assignee := addr.Address
	task.AssignedTo = &assignee
	task.UpdatedAt = s.now()
	snapshot := *task
	notifier := s.notifier
	s.mu.Unlock()

	if err := notifier.Notify(snapshot, assignee); err != nil {
		log.Printf("assignment notification failed for task %s: %v", id, err)
	}
	return &snapshot, nil
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

	// Apply sorting
	s.sortTasks(tasks, query.SortOrder)

	// Apply pagination
	page, next, total := paginate(tasks, query.PageSize, query.PageToken)

	return &models.ListTasksResponse{
		Tasks:         page,
		NextPageToken: next,
		TotalCount:    total,
	}, nil
}

// GetTask retrieves a task by ID
func (s *TaskService) GetTask(id string) (*models.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, exists := s.tasks[id]
	if !exists {
		return nil, &TaskNotFoundError{ID: id}
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
		return nil, &TaskNotFoundError{ID: id}
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
		return &TaskNotFoundError{ID: id}
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