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
	clock    func() time.Time
	notifier Notifier
}

// ErrInvalidEmail is returned (wrapped) when an email address fails validation.
var ErrInvalidEmail = errors.New("invalid email")

// TaskNotFoundError is returned when a task ID does not exist. Its message
// format ("task with ID %s not found") matches the errors returned elsewhere.
type TaskNotFoundError struct {
	ID string
}

func (e *TaskNotFoundError) Error() string {
	return fmt.Sprintf("task with ID %s not found", e.ID)
}

// Notifier is told when a task is assigned.
//
// NOTE: this is new behaviour; the service had no notification mechanism
// before and there is no prior equivalent to preserve.
type Notifier interface {
	NotifyAssigned(task models.Task, assignee string) error
}

// LogNotifier is the default Notifier; it only writes a line to the standard
// logger. New behaviour with no prior equivalent.
type LogNotifier struct{}

// NotifyAssigned logs the assignment.
func (LogNotifier) NotifyAssigned(task models.Task, assignee string) error {
	log.Printf("task %s (%q) assigned to %s", task.ID, task.Title, assignee)
	return nil
}

// Option customizes a TaskService at construction time.
type Option func(*TaskService)

// WithClock sets the time source used by the service (default time.Now).
func WithClock(clock func() time.Time) Option {
	return func(s *TaskService) {
		if clock != nil {
			s.clock = clock
		}
	}
}

// WithNotifier sets the Notifier used on assignment (default LogNotifier).
func WithNotifier(n Notifier) Option {
	return func(s *TaskService) {
		if n != nil {
			s.notifier = n
		}
	}
}

// NewTaskService creates a new task service instance
func NewTaskService(opts ...Option) *TaskService {
	s := &TaskService{
		tasks:    make(map[string]*models.Task),
		clock:    time.Now,
		notifier: LogNotifier{},
	}
	for _, opt := range opts {
		opt(s)
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
	// Negative tokens are treated as the first page for this new endpoint
	// (ListTasks keeps its original behaviour).
	token := query.PageToken
	if idx, err := strconv.Atoi(token); err == nil && idx < 0 {
		token = ""
	}
	page, next, total := paginate(tasks, query.PageSize, token)
	return &models.ListTasksResponse{
		Tasks:         page,
		NextPageToken: next,
		TotalCount:    total,
	}, nil
}

// paginate applies page_size/page_token semantics to an already filtered and
// sorted slice. The token is the numeric start offset. It returns the page,
// the next page token ("" on the last page) and the total count.
func paginate(tasks []*models.Task, pageSize int, pageToken string) ([]models.Task, string, int) {
	totalCount := len(tasks)
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	startIndex := 0
	if pageToken != "" {
		if idx, err := strconv.Atoi(pageToken); err == nil {
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
	return page, next, totalCount
}

// ListOverdueTasks returns tasks whose due date is before the current clock
// time and whose status is neither completed nor cancelled. Results are sorted
// by due date ascending (ties by ID) and paginated like ListTasks. The status,
// assigned_to and tags filters are honoured; sort_order is ignored.
func (s *TaskService) ListOverdueTasks(query models.ListTasksQuery) (*models.ListTasksResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.clock()
	var tasks []*models.Task
	for _, task := range s.tasks {
		if task.DueDate == nil || !task.DueDate.Before(now) {
			continue
		}
		if task.Status == models.TaskStatusCompleted || task.Status == models.TaskStatusCancelled {
			continue
		}
		tasks = append(tasks, task)
	}

	tasks = s.filterTasks(tasks, query)
	sort.Slice(tasks, func(i, j int) bool {
		if !tasks[i].DueDate.Equal(*tasks[j].DueDate) {
			return tasks[i].DueDate.Before(*tasks[j].DueDate)
		}
		return tasks[i].ID < tasks[j].ID
	})

	// Negative tokens are treated as the first page for this new endpoint
	// (ListTasks keeps its original behaviour).
	token := query.PageToken
	if idx, err := strconv.Atoi(token); err == nil && idx < 0 {
		token = ""
	}
	page, next, total := paginate(tasks, query.PageSize, token)
	return &models.ListTasksResponse{Tasks: page, NextPageToken: next, TotalCount: total}, nil
}

// AssignTask assigns a task to the given email address, updates UpdatedAt via
// the service clock and then notifies the Notifier. A notifier failure is
// logged and does not fail the assignment. New behaviour with no prior
// equivalent.
func (s *TaskService) AssignTask(id string, email string) (*models.Task, error) {
	email = strings.TrimSpace(email)
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
	assignee := email
	task.AssignedTo = &assignee
	task.UpdatedAt = s.clock()
	snapshot := *task
	s.mu.Unlock()

	if err := s.notifier.NotifyAssigned(snapshot, email); err != nil {
		log.Printf("failed to notify assignment of task %s to %s: %v", id, email, err)
	}
	return task, nil
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
