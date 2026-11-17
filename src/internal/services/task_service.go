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

// TaskService provides task management operations (CRUD, filtering, sorting,
// pagination, search, statistics, overdue listing and assignment) over an
// in-memory store.
//
// All access to the tasks map, the clock and the notifier is guarded by mu, a
// sync.RWMutex: read-only operations take the read lock and mutating operations
// take the write lock, so a TaskService is safe for concurrent use. Data is not
// persisted and is lost when the process exits. Construct it with NewTaskService;
// the zero value has a nil map and must not be used.
type TaskService struct {
	mu       sync.RWMutex
	tasks    map[string]*models.Task
	now      func() time.Time
	notifier Notifier
}

// ErrTaskNotFound is the sentinel error returned (wrapped) by AssignTask when the
// task ID is unknown; callers can detect it with errors.Is. Other methods return
// plain formatted "not found" errors that do not wrap it.
var ErrTaskNotFound = errors.New("task not found")

// ErrInvalidEmail is the sentinel error returned (wrapped) by AssignTask when the
// assignee email cannot be parsed as an RFC 5322 address; callers can detect it
// with errors.Is.
var ErrInvalidEmail = errors.New("invalid email")

// Notifier is the hook invoked after a task has been successfully assigned.
//
// NotifyAssigned receives a snapshot copy of the task as it was right after the
// assignment (so the implementation cannot mutate the stored task) and the
// normalized assignee email address. It has no return value, so failures must be
// handled by the implementation. TaskService calls it outside its mutex, so
// implementations may be slow, but they may be called concurrently and must be
// safe for concurrent use.
type Notifier interface {
	NotifyAssigned(task models.Task, email string)
}

// LogNotifier is the default Notifier. It has no state and its only side effect
// is writing a log line through the standard logger; it sends nothing to the
// assignee.
type LogNotifier struct{}

// NotifyAssigned logs a line containing the ID of task and the email it was
// assigned to using the standard log package. It never fails and does not modify
// the task.
func (LogNotifier) NotifyAssigned(task models.Task, email string) {
	log.Printf("notification: task %s assigned to %s", task.ID, email)
}

// SetNotifier replaces the Notifier used by AssignTask. Passing nil restores the
// default LogNotifier. It takes the write lock, so it is safe to call while other
// goroutines use the service; assignments already past their critical section keep
// using the notifier they captured.
func (s *TaskService) SetNotifier(n Notifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n == nil {
		n = LogNotifier{}
	}
	s.notifier = n
}

// SetClock replaces the injectable clock used by ListOverdueTasks to decide what
// "now" is and by AssignTask to set UpdatedAt. Passing nil restores time.Now. It
// takes the write lock, so it is safe for concurrent use. CreateTask and
// UpdateTask do not use this clock and always call time.Now directly. Intended
// mainly for tests.
func (s *TaskService) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now == nil {
		now = time.Now
	}
	s.now = now
}

// NewTaskService creates a TaskService with an empty mutex-guarded in-memory
// store, the real clock (time.Now) and the default LogNotifier, then seeds it with
// sample tasks via initializeSampleData. It never returns nil.
func NewTaskService() *TaskService {
	s := &TaskService{
		tasks:    make(map[string]*models.Task),
		now:      time.Now,
		notifier: LogNotifier{},
	}
	s.initializeSampleData()
	return s
}

// initializeSampleData seeds the store with two sample tasks through CreateTask
// (a high-priority REST API task and a medium-priority gRPC task). It is called
// once from NewTaskService, before the service is shared. Errors from CreateTask
// are ignored because the sample priorities are valid.
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

// ListTasks returns a page of tasks matching the optional filters in query.
//
// It filters by status, assignee and tags (see filterTasks), sorts by
// query.SortOrder (see sortTasks) and paginates with paginate using
// query.PageSize and query.PageToken. The response carries the page, the next
// page token ("" when no more results) and TotalCount, the number of tasks that
// matched the filters before pagination. Tasks in the page are copies. It holds
// the read lock for the whole call. The returned error is currently always nil.
// Note that without a SortOrder the order of the underlying map is unspecified, so
// paging is only stable when a sort order is given.
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
	totalCount := len(tasks)
	paginated, nextPageToken := paginate(tasks, query.PageSize, query.PageToken)

	return &models.ListTasksResponse{
		Tasks:         paginated,
		NextPageToken: nextPageToken,
		TotalCount:    totalCount,
	}, nil
}

// GetTask retrieves a task by ID. It returns a "task with ID <id> not found"
// error when the ID is unknown. The read lock is held only during the lookup; the
// returned pointer refers to the stored task itself (not a copy), so callers must
// not modify it and may observe concurrent updates.
func (s *TaskService) GetTask(id string) (*models.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, exists := s.tasks[id]
	if !exists {
		return nil, fmt.Errorf("task with ID %s not found", id)
	}

	return task, nil
}

// CreateTask creates a new task from req and stores it under a freshly generated
// UUID.
//
// The task starts in the pending status, with CreatedBy set to "system" and
// CreatedAt and UpdatedAt both set to the current time (time.Now, not the
// injectable clock). An empty Priority defaults to medium and nil Tags become an
// empty slice. It returns an error "invalid priority: <p>" if the priority is not
// valid; in that case nothing is stored. It takes the write lock. Title is not
// validated here.
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

// UpdateTask applies a partial update to the task with the given id: only the
// non-nil fields of req (Title, Description, Status, Priority, Tags, AssignedTo,
// DueDate) are changed.
//
// It returns a "not found" error for an unknown id, and "invalid status: <s>" or
// "invalid priority: <p>" for invalid values. Moving to the completed status sets
// CompletedAt the first time only; it is never cleared afterwards. On success
// UpdatedAt is set to time.Now and the updated stored task is returned. It takes
// the write lock. The update is not atomic: fields are applied in order, so when a
// validation error occurs, fields handled before the invalid one (for example
// Title, Description or Status) remain modified and UpdatedAt is not refreshed.
// No assignment notification is sent from here, even if AssignedTo changes; use
// AssignTask for that.
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

// UpdateTaskStatus changes only the status of the task with the given id by
// delegating to UpdateTask. It returns the updated task, or the same errors as
// UpdateTask (not found, invalid status). It sets CompletedAt on first completion
// and refreshes UpdatedAt.
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

// SearchTasks performs a case-insensitive substring search across task titles,
// descriptions, and tags. Returns matching tasks ordered by relevance (title
// matches first, then description, then tags).
//
// Relevance is a score: 3 for a title match, 2 for a description match and 1 if
// any tag matches; matches are sorted by descending score and tasks with equal
// scores are in unspecified order. The result is limited to query.PageSize
// entries (default 20 when it is zero or negative); there is no page token, and
// TotalCount is the number of matches before that limit. An empty query matches
// every task. Returned tasks are copies, and the response echoes the original
// query text. It holds the read lock and never returns an error.
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

// DeleteTask removes the task with the given id from the store. It returns a
// "task with ID <id> not found" error if the id is unknown. It takes the write
// lock. Comments held by CommentService for the task are not removed.
func (s *TaskService) DeleteTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.tasks[id]; !exists {
		return fmt.Errorf("task with ID %s not found", id)
	}

	delete(s.tasks, id)
	return nil
}

// filterTasks returns the tasks that satisfy every filter set in query; unset
// (empty) filters are ignored. Status must equal query.Status, AssignedTo must
// equal query.AssignedTo exactly (unassigned tasks never match), and Tags is a
// comma-separated list, each entry trimmed of whitespace, of which the task must
// carry all (exact, case-sensitive match). The input slice is not modified and the
// caller must hold s.mu.
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

// sortTasks sorts tasks in place according to order: created_at, due_date or
// priority, ascending or descending. For due-date orders the comparison treats
// tasks with no due date inconsistently with a strict ordering, so their position
// is not well defined. An unrecognized or empty order leaves the slice untouched.
// The caller must hold s.mu.
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

// paginate applies offset-based pagination (default page size 20, max 100) and
// returns the page plus the next page token ("" when there are no more results).
//
// A pageSize of zero or less becomes 20 and values above 100 are capped at 100.
// pageToken is the decimal start offset produced by a previous call; an empty or
// non-numeric token starts at offset 0, and an out-of-range (negative or past the
// end) offset yields an empty page. The returned tasks are value copies, so the
// page does not alias the store. The next token is the offset following the page.
// It does not lock; callers must hold s.mu while the tasks are read.
func paginate(tasks []*models.Task, pageSize int, pageToken string) ([]models.Task, string) {
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
	if startIndex >= 0 && startIndex < totalCount {
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

// ListOverdueTasks returns tasks whose due date is before now and whose status
// is neither completed nor cancelled, sorted by due date ascending (ID tiebreak).
//
// "Now" comes from the injectable clock (see SetClock); tasks without a due date
// are never overdue. Pagination follows paginate using query.PageSize and
// query.PageToken, and TotalCount is the number of overdue tasks before
// pagination. It holds the read lock while collecting, sorting and paginating
// (the clock is read under that lock), and returns copies of the tasks. It never
// fails.
func (s *TaskService) ListOverdueTasks(query models.OverdueTasksQuery) *models.ListTasksResponse {
	s.mu.RLock()
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
		if overdue[i].DueDate.Equal(*overdue[j].DueDate) {
			return overdue[i].ID < overdue[j].ID
		}
		return overdue[i].DueDate.Before(*overdue[j].DueDate)
	})
	page, next := paginate(overdue, query.PageSize, query.PageToken)
	s.mu.RUnlock()

	return &models.ListTasksResponse{
		Tasks:         page,
		NextPageToken: next,
		TotalCount:    len(overdue),
	}
}

// AssignTask assigns the task with the given id to email and triggers a
// notification.
//
// The email is parsed with net/mail.ParseAddress; the normalized bare address
// (display name stripped) is stored in AssignedTo. It returns an error wrapping
// ErrInvalidEmail if the email cannot be parsed (checked first, before the task is
// looked up), or wrapping ErrTaskNotFound if the id is unknown. On success it sets
// UpdatedAt using the injectable clock and returns a snapshot copy of the updated
// task. The mutation happens under the write lock; the Notifier is then called
// after the lock is released, with the snapshot and the address, so a slow
// notifier cannot block other operations. Notifier panics or errors are not
// handled here.
func (s *TaskService) AssignTask(id, email string) (*models.Task, error) {
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidEmail, email)
	}

	s.mu.Lock()
	task, exists := s.tasks[id]
	if !exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: task with ID %s not found", ErrTaskNotFound, id)
	}
	assignee := addr.Address
	task.AssignedTo = &assignee
	task.UpdatedAt = s.now()
	snapshot := *task
	notifier := s.notifier
	s.mu.Unlock()

	// Notify outside the lock so a slow notifier cannot block the service.
	notifier.NotifyAssigned(snapshot, assignee)
	return &snapshot, nil
}
