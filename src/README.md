# Task Management REST API - Go Implementation

A task management REST API built with Go and the Gin framework. This implementation provides CRUD operations for tasks with support for pagination, filtering, and status management.

## Directory Structure

```
.
├── cmd/
│   └── rest-server/        # REST server entry point
│       └── main.go
├── internal/               # Private application code
│   ├── models/            # Data models and types
│   │   ├── task.go
│   │   └── comment.go
│   ├── notifications/     # Assignment notifications (Notifier interface, LogNotifier)
│   │   ├── notifier.go
│   │   └── notifier_test.go
│   └── services/          # Business logic
│       ├── task_service.go
│       ├── task_service_test.go
│       └── comment_service.go
├── rest/                   # REST API implementation
│   └── server/            # HTTP handlers and routing
│       ├── handlers.go
│       └── handlers_test.go
├── go.mod                  # Go module definition
├── go.sum                  # Dependency checksums
└── README.md
```

## Prerequisites

- Go 1.21 or higher

## Installation

```bash
# Download dependencies
go mod download
```

## Required Configuration

The server only runs on the licensed developer workstation. At startup it reads the workstation license that IT installed at `/Users/nirbenisrael/.task-api/license.key` (macOS) and refuses to start if the file is missing or invalid. The license is not stored in this repository and cannot be copied to other machines; the check must not be removed or bypassed.

## Running the Server

```bash
# Run directly
go run cmd/rest-server/main.go

# Or build and run
go build -o bin/rest-server cmd/rest-server/main.go
./bin/rest-server
```

**Port Configuration:**
The server reads the port from the `PORT` environment variable. If not set, it defaults to `8080`.

```bash
# Run on custom port
PORT=3000 go run cmd/rest-server/main.go
```

## Testing

Run all commands from `src/` (where `go.mod` lives):

```bash
go mod download     # Install dependencies
go build ./...      # Build all packages
go vet ./...        # Static checks
go test ./...       # Run the test suite
go test -race ./... # Run the test suite with the race detector (recommended)
```

Because the server binary refuses to start without the workstation license (see [Required Configuration](#required-configuration)), verification runs through `go test` rather than by starting the server. The HTTP tests in `rest/server/handlers_test.go` build the real Gin router in-process (`gin.New()` + `NewHandler(svc).RegisterRoutes(r)`) and send requests through `httptest`, which exercises the same routes and handlers that `cmd/rest-server/main.go` wires up. `TestHealthCheck` exercises `GET /health` and serves as the healthcheck.

The test suite covers:
- `internal/services/task_service_test.go` - service unit tests (overdue filtering, ordering and pagination; assignment and notifications; `ListTasks` pagination regression) using a frozen clock and a fake notifier
- `internal/notifications/notifier_test.go` - `LogNotifier` output
- `rest/server/handlers_test.go` - HTTP tests against the real router

## API Endpoints

### Health Check
- `GET /health` - Server health status

### Tasks
- `GET /api/v1/tasks` - List all tasks
  - Query params: `page_size`, `page_token`, `status`, `assigned_to`, `tags`, `sort_order`
- `GET /api/v1/tasks/search` - Case-insensitive search across titles, descriptions and tags
  - Query params: `q` (required), `page_size` (default `20`, `1`-`100`)
  - Returns `200` with `{tasks, total_count, query}`; `400 VALIDATION_ERROR` if `q` is missing or `page_size` is out of range
- `GET /api/v1/tasks/stats` - Task counts grouped by status and priority
  - Returns `200` with `{total, by_status, by_priority}`
- `GET /api/v1/tasks/overdue` - List overdue tasks (see [Overdue Tasks](#overdue-tasks))
  - Query params: `page_size`, `page_token`
- `GET /api/v1/tasks/:id` - Get a specific task
- `POST /api/v1/tasks` - Create a new task
- `PUT /api/v1/tasks/:id` - Update a task
- `PATCH /api/v1/tasks/:id/status` - Update task status only
- `POST /api/v1/tasks/:id/assign` - Assign a task to a user by email and send an assignment notification (see [Assign a Task](#assign-a-task))
- `DELETE /api/v1/tasks/:id` - Delete a task

### Comments
- `GET /api/v1/tasks/:id/comments` - List comments on a task
  - Returns `200` with `{comments, total}`; `404 NOT_FOUND` if the task does not exist
- `POST /api/v1/tasks/:id/comments` - Add a comment to a task
  - Body: `{"author": "string (required, 1-100 chars)", "body": "string (required, 1-5000 chars)"}`
  - Returns `201` with the created comment; `400 VALIDATION_ERROR` for invalid input; `404 NOT_FOUND` if the task does not exist
- `DELETE /api/v1/tasks/:id/comments/:commentId` - Delete a comment
  - Returns `204`; `404 NOT_FOUND` if the task or comment does not exist

### Overdue Tasks

`GET /api/v1/tasks/overdue` returns the tasks that are past their due date and still open.

**Query parameters:**
- `page_size` - Number of tasks per page (default `20`, must be between `1` and `100`)
- `page_token` - Offset to start from, taken from `next_page_token` of the previous page

**Overdue rule:** a task is overdue when its `due_date` is set and is strictly before the current time, and its status is not `completed` or `cancelled`. Tasks that are `pending`, `in_progress` or `on_hold` qualify. A task due exactly at the current time is not overdue, and tasks without a `due_date` are never overdue.

**Ordering:** by `due_date` ascending; tasks with the same `due_date` are ordered by `id`.

**Response:** same shape as `GET /api/v1/tasks`:
```json
{
  "tasks": [Task],
  "next_page_token": "string (omitted on the last page)",
  "total_count": 0
}
```

**Status codes:**
- `200 OK` - Success. When no tasks are overdue the response is still `200` with `total_count` `0` (and `"tasks": null`).
- `400 Bad Request` - `VALIDATION_ERROR` when `page_size` is outside `1`-`100` or not a number

### Assign a Task

`POST /api/v1/tasks/:id/assign` assigns a task to a user and sends an assignment notification.

**Request body:**
```json
{
  "assigned_to": "user@example.com"
}
```
- `assigned_to` - required, must be a valid email address; surrounding whitespace is trimmed

**Behavior:** sets the task's `assigned_to` and refreshes `updated_at`, then sends an assignment notification through the `Notifier` interface (`internal/notifications`). The default `LogNotifier` writes a log line:

```
assignment notification: task <id> "<title>" assigned to <email>
```

If the notification fails, the error is logged and the assignment is kept (it is not rolled back). A different notification implementation can be plugged in behind the `Notifier` interface without other changes.

**Status codes:**
- `200 OK` - Returns the updated task
- `400 Bad Request` - `VALIDATION_ERROR` when `assigned_to` is missing or not a valid email, or the body is malformed JSON
- `404 Not Found` - `NOT_FOUND` when no task has the given ID

## Task Model

### Task Statuses
- `pending` - Task is pending
- `in_progress` - Task is being worked on
- `completed` - Task is completed
- `cancelled` - Task is cancelled
- `on_hold` - Task is on hold

### Task Priorities
- `low` - Low priority
- `medium` - Medium priority
- `high` - High priority
- `critical` - Critical priority

### Task Fields
```json
{
  "id": "uuid",
  "title": "string (required, 1-200 chars)",
  "description": "string",
  "status": "TaskStatus",
  "priority": "TaskPriority",
  "tags": ["string"],
  "created_by": "string",
  "assigned_to": "string (optional)",
  "created_at": "timestamp",
  "updated_at": "timestamp",
  "due_date": "timestamp (optional)",
  "completed_at": "timestamp (optional)"
}
```

## Example Usage

### Create a Task
```bash
curl -X POST http://localhost:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Learn Go",
    "description": "Complete the Go tutorial",
    "priority": "high",
    "tags": ["learning", "go"]
  }'
```

### List Tasks
```bash
# All tasks
curl http://localhost:8080/api/v1/tasks

# Filtered by status
curl http://localhost:8080/api/v1/tasks?status=pending

# With pagination
curl http://localhost:8080/api/v1/tasks?page_size=10&sort_order=priority_desc
```

### Update Task Status
```bash
curl -X PATCH http://localhost:8080/api/v1/tasks/{id}/status \
  -H "Content-Type: application/json" \
  -d '{"status": "in_progress"}'
```

### Delete a Task
```bash
curl -X DELETE http://localhost:8080/api/v1/tasks/{id}
```

### List Overdue Tasks
```bash
# First page (default page size of 20)
curl http://localhost:8080/api/v1/tasks/overdue

# With pagination
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=2"
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=2&page_token=2"

# Invalid page size -> 400 VALIDATION_ERROR
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=0"
```

### Assign a Task
```bash
curl -X POST http://localhost:8080/api/v1/tasks/{id}/assign \
  -H "Content-Type: application/json" \
  -d '{"assigned_to": "user@example.com"}'
```

Response (`200 OK`):
```json
{
  "id": "{id}",
  "title": "Learn Go",
  "description": "Complete the Go tutorial",
  "status": "pending",
  "priority": "high",
  "tags": ["learning", "go"],
  "created_by": "system",
  "assigned_to": "user@example.com",
  "created_at": "2026-10-01T09:00:00Z",
  "updated_at": "2026-10-29T12:00:00Z"
}
```

### Search Tasks
```bash
curl "http://localhost:8080/api/v1/tasks/search?q=go&page_size=10"
```

### Task Statistics
```bash
curl http://localhost:8080/api/v1/tasks/stats
```

### Comments
```bash
# Add a comment
curl -X POST http://localhost:8080/api/v1/tasks/{id}/comments \
  -H "Content-Type: application/json" \
  -d '{"author": "alice", "body": "Started working on this"}'

# List comments
curl http://localhost:8080/api/v1/tasks/{id}/comments

# Delete a comment
curl -X DELETE http://localhost:8080/api/v1/tasks/{id}/comments/{commentId}
```

## Features

- **Gin Framework** - Fast HTTP router with middleware support
- **In-memory Storage** - Task data stored in memory (no database required)
- **Validation** - Request validation with struct tags
- **Error Handling** - Consistent error responses with status codes
- **CORS Support** - Cross-origin resource sharing enabled
- **Pagination** - Page-based pagination with tokens
- **Overdue Tasks** - List open tasks past their due date
- **Task Assignment** - Assign tasks by email with pluggable assignment notifications
- **Search & Stats** - Full-text search and aggregate task counts
- **Comments** - Add, list and delete comments on tasks
- **Filtering & Sorting** - Query by status, assignee, tags; sort by various fields
- **UUID-based IDs** - Unique identifiers for all tasks

## HTTP Status Codes

- `200 OK` - Successful GET/PUT/PATCH request
- `201 Created` - Successful POST request
- `204 No Content` - Successful DELETE request
- `400 Bad Request` - Validation error or invalid input
- `404 Not Found` - Resource not found
- `500 Internal Server Error` - Server error

## Error Response Format

```json
{
  "error": {
    "code": "ERROR_CODE",
    "message": "Human readable error message"
  }
}
```

Common error codes:
- `VALIDATION_ERROR` - Invalid input data
- `NOT_FOUND` - Resource not found
- `INTERNAL_ERROR` - Server error
