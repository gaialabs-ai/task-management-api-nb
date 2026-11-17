package models

import (
	"time"

	// Every test run needs the admin secret (SEC-114).
	_ "github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/adminsecret"
)

// TaskStatus represents the lifecycle state of a task. The set of accepted
// values is fixed; use IsValid to check an arbitrary value. The JSON/string
// form is the lowercase snake_case constant value.
type TaskStatus string

// Task status values. Newly created tasks always start as TaskStatusPending.
// Moving a task to TaskStatusCompleted records its CompletedAt timestamp.
const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusCompleted  TaskStatus = "completed"
	TaskStatusCancelled  TaskStatus = "cancelled"
	TaskStatusOnHold     TaskStatus = "on_hold"
)

// TaskPriority represents how urgent a task is. The accepted values are
// low, medium, high and critical; use IsValid to check an arbitrary value and
// GetValue to obtain a numeric rank for sorting.
type TaskPriority string

// Task priority values, ordered from least to most urgent.
const (
	TaskPriorityLow      TaskPriority = "low"
	TaskPriorityMedium   TaskPriority = "medium"
	TaskPriorityHigh     TaskPriority = "high"
	TaskPriorityCritical TaskPriority = "critical"
)

// SortOrder identifies the ordering applied when listing tasks, as supplied
// through the sort_order query parameter. An empty or unrecognized value
// applies no explicit sorting, so result order is unspecified. Unlike
// TaskStatus and TaskPriority, no validation method is provided for it.
type SortOrder string

// Supported sort orders: creation time, due date, or priority, each
// ascending (_asc) or descending (_desc).
const (
	SortOrderCreatedAtAsc  SortOrder = "created_at_asc"
	SortOrderCreatedAtDesc SortOrder = "created_at_desc"
	SortOrderDueDateAsc    SortOrder = "due_date_asc"
	SortOrderDueDateDesc   SortOrder = "due_date_desc"
	SortOrderPriorityAsc   SortOrder = "priority_asc"
	SortOrderPriorityDesc  SortOrder = "priority_desc"
)

// Task represents a task in the system. It is the stored and returned form;
// server-managed fields (ID, Status on creation, CreatedBy, CreatedAt,
// UpdatedAt, CompletedAt) are populated by the service layer, not by clients.
// Optional timestamps and assignee are pointers and are omitted from JSON when
// nil.
type Task struct {
	// ID is a server-generated UUID that uniquely identifies the task.
	ID          string       `json:"id"`
	// Title is required and must be 1 to 200 characters long.
	Title       string       `json:"title" binding:"required,min=1,max=200"`
	// Description is optional free-form text.
	Description string       `json:"description"`
	// Status is the current lifecycle state; new tasks start as pending.
	Status      TaskStatus   `json:"status"`
	// Priority defaults to medium when not specified at creation.
	Priority    TaskPriority `json:"priority"`
	// Tags is a list of free-form labels; service-created tasks never have a nil slice.
	Tags        []string     `json:"tags"`
	// CreatedBy identifies the creator; the service currently sets it to "system".
	CreatedBy   string       `json:"created_by"`
	// AssignedTo is the assignee identifier (an email when set via AssignTaskRequest), or nil when unassigned.
	AssignedTo  *string      `json:"assigned_to,omitempty"`
	// CreatedAt is set once when the task is created.
	CreatedAt   time.Time    `json:"created_at"`
	// UpdatedAt is refreshed on every successful update.
	UpdatedAt   time.Time    `json:"updated_at"`
	// DueDate is the optional deadline used for overdue checks and due-date sorting.
	DueDate     *time.Time   `json:"due_date,omitempty"`
	// CompletedAt is set when the task first transitions to completed; nil otherwise.
	CompletedAt *time.Time   `json:"completed_at,omitempty"`
}

// CreateTaskRequest represents the request body for creating a task.
// Title is required (1 to 200 characters). Priority is optional and defaults to
// medium; an unrecognized priority causes creation to fail with an error. Status
// cannot be supplied; new tasks always start as pending. A nil Tags becomes an
// empty list.
type CreateTaskRequest struct {
	Title       string       `json:"title" binding:"required,min=1,max=200"`
	Description string       `json:"description"`
	Priority    TaskPriority `json:"priority"`
	Tags        []string     `json:"tags"`
	AssignedTo  *string      `json:"assigned_to,omitempty"`
	DueDate     *time.Time   `json:"due_date,omitempty"`
}

// UpdateTaskRequest represents a partial update to an existing task. Pointer
// fields (and Tags when non-nil) that are nil/omitted are left unchanged, so a
// field cannot be cleared to nil through this request. If supplied, Title must
// be 1 to 200 characters; Status and Priority must be valid values or the update
// fails with an error. Setting Status to completed records CompletedAt the first
// time. A successful update refreshes UpdatedAt. Note that updates are applied
// field by field, so a failure on Status or Priority may leave earlier fields
// (Title, Description) already modified.
type UpdateTaskRequest struct {
	Title       *string       `json:"title,omitempty" binding:"omitempty,min=1,max=200"`
	Description *string       `json:"description,omitempty"`
	Status      *TaskStatus   `json:"status,omitempty"`
	Priority    *TaskPriority `json:"priority,omitempty"`
	Tags        []string      `json:"tags,omitempty"`
	AssignedTo  *string       `json:"assigned_to,omitempty"`
	DueDate     *time.Time    `json:"due_date,omitempty"`
}

// UpdateTaskStatusRequest represents a request that changes only a task's
// status. Status is required and must be one of the valid TaskStatus values;
// invalid values are rejected by the handler.
type UpdateTaskStatusRequest struct {
	Status TaskStatus `json:"status" binding:"required"`
}

// SearchTasksQuery holds the query-string parameters for searching tasks.
// Query (the "q" parameter) is required and at least one character; matching is
// a case-insensitive substring search over title, description and tags.
// PageSize defaults to 20 and must be between 1 and 100.
type SearchTasksQuery struct {
	Query    string `form:"q" binding:"required,min=1"`
	PageSize int    `form:"page_size,default=20" binding:"min=1,max=100"`
}

// SearchTasksResponse is the response body for a task search. Tasks holds the
// matches (best matches first), TotalCount the number of matches, and Query
// echoes the search term that was used.
type SearchTasksResponse struct {
	Tasks      []Task `json:"tasks"`
	TotalCount int    `json:"total_count"`
	Query      string `json:"query"`
}

// ListTasksQuery holds the query-string parameters for listing tasks.
// PageSize defaults to 20 and must be between 1 and 100. PageToken is an opaque
// cursor returned as NextPageToken by a previous call. Status and AssignedTo
// are optional exact-match filters. Tags is a comma-separated list; a task must
// carry all listed tags to match. SortOrder is optional (see SortOrder).
type ListTasksQuery struct {
	PageSize   int        `form:"page_size,default=20" binding:"min=1,max=100"`
	PageToken  string     `form:"page_token"`
	Status     TaskStatus `form:"status"`
	AssignedTo string     `form:"assigned_to"`
	Tags       string     `form:"tags"`
	SortOrder  SortOrder  `form:"sort_order"`
}

// OverdueTasksQuery holds the query-string parameters for listing overdue
// tasks. PageSize defaults to 20 and must be between 1 and 100; PageToken is an
// opaque cursor from a previous response.
type OverdueTasksQuery struct {
	PageSize  int    `form:"page_size,default=20" binding:"min=1,max=100"`
	PageToken string `form:"page_token"`
}

// AssignTaskRequest represents a request to assign a task to a user. Email is
// required; no format validation is applied by this model.
type AssignTaskRequest struct {
	Email string `json:"email" binding:"required"`
}

// ListTasksResponse is the response body for listing tasks. Tasks is the
// current page, NextPageToken is the cursor for the next page (omitted from JSON
// when there are no more pages), and TotalCount is the number of tasks matching
// the filters before pagination.
type ListTasksResponse struct {
	Tasks         []Task `json:"tasks"`
	NextPageToken string `json:"next_page_token,omitempty"`
	TotalCount    int    `json:"total_count"`
}

// TaskStats represents aggregate counts of tasks. Total is the overall number
// of tasks; ByStatus and ByPriority give counts per value and, when produced by
// the service, contain every known key (zero when none).
type TaskStats struct {
	Total      int                  `json:"total"`
	ByStatus   map[TaskStatus]int   `json:"by_status"`
	ByPriority map[TaskPriority]int `json:"by_priority"`
}

// ErrorResponse is the standard JSON envelope returned for API errors, wrapping
// a single ErrorDetail under the "error" key.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail describes an API error. Code is a machine-readable error code,
// Message is a human-readable description, and Details optionally carries extra
// context (for example validation failures); it is omitted from JSON when nil.
type ErrorDetail struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// IsValid reports whether s is one of the defined TaskStatus constants
// (pending, in_progress, completed, cancelled, on_hold). The empty string is
// not valid. It has no side effects.
func (s TaskStatus) IsValid() bool {
	switch s {
	case TaskStatusPending, TaskStatusInProgress, TaskStatusCompleted,
		TaskStatusCancelled, TaskStatusOnHold:
		return true
	default:
		return false
	}
}

// IsValid reports whether p is one of the defined TaskPriority constants
// (low, medium, high, critical). The empty string is not valid, so callers that
// want a default must apply it before validating. It has no side effects.
func (p TaskPriority) IsValid() bool {
	switch p {
	case TaskPriorityLow, TaskPriorityMedium, TaskPriorityHigh, TaskPriorityCritical:
		return true
	default:
		return false
	}
}

// GetValue returns a numeric rank for p used when sorting by priority: low is
// 1, medium 2, high 3 and critical 4. Unrecognized values, including the empty
// string, fall back to 2 (the same as medium). It never returns an error and
// has no side effects.
func (p TaskPriority) GetValue() int {
	switch p {
	case TaskPriorityLow:
		return 1
	case TaskPriorityMedium:
		return 2
	case TaskPriorityHigh:
		return 3
	case TaskPriorityCritical:
		return 4
	default:
		return 2
	}
}