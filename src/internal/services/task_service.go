package services

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/notifications"
	"github.com/google/uuid"
)

// TaskService provides task management operations
type TaskService struct {
	mu       sync.RWMutex
	tasks    map[string]*models.Task
	now      func() time.Time
	notifier notifications.Notifier

	// skipSampleData is set by WithoutSampleData and only read at construction
	skipSampleData bool
}

// Option configures a TaskService created by NewTaskServiceWithOptions
type Option func(*TaskService)

// WithClock sets the clock used to evaluate overdue tasks and assignment
// timestamps. A nil clock is ignored and the default (time.Now) is kept.
func WithClock(now func() time.Time) Option {
	return func(s *TaskService) {
		if now != nil {
			s.now = now
		}
	}
}

// WithNotifier sets the notifier used for assignment notifications. A nil
// notifier is ignored and the default (LogNotifier) is kept.
func WithNotifier(n notifications.Notifier) Option {
	return func(s *TaskService) {
		if n != nil {
			s.notifier = n
		}
	}
}

// WithoutSampleData creates the service with an empty task store
func WithoutSampleData() Option {
	return func(s *TaskService) {
		s.skipSampleData = true
	}
}

// NewTaskService creates a new task service instance
func NewTaskService() *TaskService {
	return NewTaskServiceWithOptions()
}

// NewTaskServiceWithOptions creates a new task service instance configured by
// the given options. Without options it behaves exactly like NewTaskService:
// it uses the real clock, logs assignment notifications and seeds sample data.
func NewTaskServiceWithOptions(opts ...Option) *TaskService {
	s := &TaskService{
		tasks:    make(map[string]*models.Task),
		now:      time.Now,
		notifier: notifications.NewLogNotifier(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if !s.skipSampleData {
		s.initializeSampleData()
	}
	return s
}

// SetNotifier replaces the notifier used for assignment notifications.
// A nil notifier is ignored.
func (s *TaskService) SetNotifier(n notifications.Notifier) {
	if n == nil {
		return
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
	return paginate(tasks, query.PageSize, query.PageToken), nil
}

// paginate applies offset-based pagination to an ordered task list. The page
// size defaults to 20 and is capped at 100; the page token is the string offset
// of the first task on the page.
func paginate(tasks []*models.Task, pageSize int, pageToken string) *models.ListTasksResponse {
	totalCount := len(tasks)
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	startIndex := 0
	if pageToken != "" {
		// Negative offsets are ignored so they cannot slice out of range
		if idx, err := strconv.Atoi(pageToken); err == nil && idx >= 0 {
			startIndex = idx
		}
	}

	endIndex := startIndex + pageSize
	if endIndex > totalCount {
		endIndex = totalCount
	}

	var paginatedTasks []models.Task
	if startIndex < totalCount {
		for _, task := range tasks[startIndex:endIndex] {
			paginatedTasks = append(paginatedTasks, *task)
		}
	}

	var nextPageToken string
	if endIndex < totalCount {
		nextPageToken = strconv.Itoa(endIndex)
	}

	return &models.ListTasksResponse{
		Tasks:         paginatedTasks,
		NextPageToken: nextPageToken,
		TotalCount:    totalCount,
	}
}

// ListOverdueTasks returns a paginated list of overdue tasks. A task is overdue
// when its due date is strictly before the current time and its status is
// neither completed nor cancelled. Results are ordered by due date ascending,
// with the task ID as a tiebreaker.
func (s *TaskService) ListOverdueTasks(query models.ListOverdueTasksQuery) (*models.ListTasksResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.now()
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

	sort.Slice(tasks, func(i, j int) bool {
		if !tasks[i].DueDate.Equal(*tasks[j].DueDate) {
			return tasks[i].DueDate.Before(*tasks[j].DueDate)
		}
		return tasks[i].ID < tasks[j].ID
	})

	response := paginate(tasks, query.PageSize, query.PageToken)
	// Serialize an empty page as [] rather than null
	if response.Tasks == nil {
		response.Tasks = []models.Task{}
	}
	return response, nil
}

// AssignTask assigns a task to the given email address and sends an assignment
// notification. The notification is sent after the lock is released; a
// notification failure is logged and does not undo the assignment.
func (s *TaskService) AssignTask(id, email string) (*models.Task, error) {
	email = strings.TrimSpace(email)

	s.mu.Lock()
	task, exists := s.tasks[id]
	if !exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("task with ID %s not found", id)
	}

	assignee := email
	task.AssignedTo = &assignee
	task.UpdatedAt = s.now()

	// Copy the task so it can be used safely after the lock is released
	assigned := *task
	if task.Tags != nil {
		assigned.Tags = append([]string(nil), task.Tags...)
	}
	notifier := s.notifier
	s.mu.Unlock()

	if notifier != nil {
		if err := notifier.NotifyAssignment(assigned, email); err != nil {
			log.Printf("failed to send assignment notification for task %s: %v", id, err)
		}
	}

	return &assigned, nil
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