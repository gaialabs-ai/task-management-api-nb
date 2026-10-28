# Task Management REST API - Go Implementation

A task management REST API built with Go and the Gin framework. This implementation provides CRUD operations for tasks with support for pagination, filtering, and status management.

## Directory Structure

```
.
├── cmd/
│   └── rest-server/              # REST server entry point (license check, Gin setup)
│       └── main.go
├── internal/                     # Private application code
│   ├── models/                   # Data models and request/response types
│   │   ├── comment.go
│   │   └── task.go
│   ├── notifications/            # Notifier interface and default LogNotifier
│   │   ├── notifier.go
│   │   └── notifier_test.go
│   ├── services/                 # Business logic (in-memory storage)
│   │   ├── comment_service.go
│   │   ├── task_service.go
│   │   └── task_service_test.go
│   └── testutil/                 # Shared test helpers (frozen clock, recording notifier)
│       └── testutil.go
├── rest/                         # REST API implementation
│   └── server/                   # HTTP handlers and routing
│       ├── handlers.go
│       └── handlers_test.go
├── go.mod                        # Go module definition
├── go.sum                        # Dependency checksums
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

**Gin Mode:**
If `GIN_MODE` is not set, the server runs Gin in `debug` mode. Set `GIN_MODE=release` to use release mode.

```bash
GIN_MODE=release PORT=3000 go run cmd/rest-server/main.go
```

## Testing

Run all commands from `src/` (where `go.mod` lives):

```bash
go build ./...      # Compile all packages
go vet ./...        # Static checks
go test ./...       # Run the test suite
go test -race ./... # Run the test suite with the race detector (recommended)
```

The test suite does not need the server to be running, so it also works on machines without the workstation license (see [Required Configuration](#required-configuration)). On the licensed workstation you can additionally start the server (`go run ./cmd/rest-server`) and call `curl http://localhost:8080/health`. The HTTP tests in `rest/server/handlers_test.go` build an in-process Gin engine (`gin.New()` + `server.NewHandler(svc).RegisterRoutes(r)`) and send requests through `httptest`. They exercise the same router and handlers that `main.go` wires up, including `GET /health`. The tests use only the standard `testing` package:

- `internal/services/task_service_test.go` - service unit tests (overdue filtering, sorting and pagination, assignment and notifications, `ListTasks` pagination regression) using a frozen clock and a recording fake notifier
- `internal/notifications/notifier_test.go` - `LogNotifier` output
- `rest/server/handlers_test.go` - HTTP tests for the routes, status codes and error envelopes

## API Endpoints

### Health Check
- `GET /health` - Server health status

### Tasks
- `GET /api/v1/tasks` - List all tasks
  - Query params: `page_size`, `page_token`, `status`, `assigned_to`, `tags`, `sort_order`
- `GET /api/v1/tasks/search` - Search tasks by title, description and tags (case-insensitive)
  - Query params: `q` (required), `page_size` (default 20, 1-100)
  - Response: `{tasks, total_count, query}`, ordered by relevance (title matches first, then description, then tags)
- `GET /api/v1/tasks/stats` - Task counts: `{total, by_status, by_priority}`
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
  - Response: `{comments, total}`; 404 `NOT_FOUND` if the task does not exist
- `POST /api/v1/tasks/:id/comments` - Add a comment to a task
  - Body: `{"author": "string (required, 1-100 chars)", "body": "string (required, 1-5000 chars)"}`
  - Returns `201 Created` with the comment; 400 `VALIDATION_ERROR` for invalid input; 404 `NOT_FOUND` if the task does not exist
- `DELETE /api/v1/tasks/:id/comments/:commentId` - Delete a comment
  - Returns `204 No Content`; 404 `NOT_FOUND` if the task or comment does not exist

### Overdue Tasks

`GET /api/v1/tasks/overdue` returns tasks that are past their due date and still open.

**Overdue rule:** a task is overdue when:
- `due_date` is set **and** strictly before the current time (a task due exactly now is not overdue), and
- `status` is not `completed` or `cancelled`. Only `pending`, `in_progress` and `on_hold` tasks qualify.

**Sorting:** by `due_date` ascending (oldest first). Tasks with the same `due_date` are ordered by `id`.

**Query parameters** (same pagination as `GET /api/v1/tasks`):
- `page_size` - number of tasks per page (default `20`, min `1`, max `100`)
- `page_token` - offset token taken from `next_page_token` of the previous page
  (a non-numeric or negative token is ignored and the first page is returned; previously a negative token on `GET /api/v1/tasks` caused a 500)

**Response** (`200 OK`): `{tasks, next_page_token?, total_count}`
- `next_page_token` is only present when more results remain
- `total_count` is the total number of overdue tasks across all pages
- If no tasks are overdue, the response is still `200 OK` with `"tasks": []` and `total_count: 0`

**Errors:**
- `400 Bad Request` / `VALIDATION_ERROR` - invalid `page_size` (e.g. `0`, `101` or not a number)

### Assign a Task

`POST /api/v1/tasks/:id/assign` assigns a task to a user and sends an assignment notification.

**Request body:**
```json
{
  "assigned_to": "user@example.com"
}
```
- `assigned_to` - required, must be a valid email address

**Response** (`200 OK`): the updated task, with `assigned_to` set and `updated_at` refreshed.

**Notification:** after a successful assignment, the service sends an assignment notification through the `Notifier` interface (`internal/notifications`). The default implementation, `LogNotifier`, writes a log line:

```
assignment notification: task <id> "<title>" assigned to <email>
```

If the notifier returns an error, the error is logged and the assignment is **not** rolled back. The request still returns `200 OK`. A different notification implementation can be added behind the `Notifier` interface without changing the endpoint.

**Errors:**
- `400 Bad Request` / `VALIDATION_ERROR` - missing `assigned_to`, invalid email address, or malformed JSON
- `404 Not Found` / `NOT_FOUND` - no task with the given ID

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

# Two tasks per page, then the next page
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=2"
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=2&page_token=2"
```

Sample response:
```json
{
  "tasks": [
    {
      "id": "3f1c2a9e-8b7d-4c1e-9a2f-1d4e5b6c7a80",
      "title": "Submit quarterly report",
      "description": "Finance needs the Q3 numbers",
      "status": "in_progress",
      "priority": "high",
      "tags": ["finance"],
      "created_by": "system",
      "created_at": "2026-10-01T09:00:00Z",
      "updated_at": "2026-10-10T12:30:00Z",
      "due_date": "2026-10-15T17:00:00Z"
    },
    {
      "id": "7a2b4c6d-1e3f-4a5b-8c9d-0e1f2a3b4c5d",
      "title": "Renew SSL certificate",
      "description": "",
      "status": "pending",
      "priority": "critical",
      "tags": ["ops"],
      "created_by": "system",
      "assigned_to": "ops@example.com",
      "created_at": "2026-10-05T08:00:00Z",
      "updated_at": "2026-10-05T08:00:00Z",
      "due_date": "2026-10-20T00:00:00Z"
    }
  ],
  "next_page_token": "2",
  "total_count": 3
}
```

When nothing is overdue:
```json
{
  "tasks": [],
  "total_count": 0
}
```

### Assign a Task
```bash
curl -X POST http://localhost:8080/api/v1/tasks/{id}/assign \
  -H "Content-Type: application/json" \
  -d '{"assigned_to": "user@example.com"}'
```

An invalid email returns `400 Bad Request`:
```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Key: 'AssignTaskRequest.AssignedTo' Error:Field validation for 'AssignedTo' failed on the 'email' tag"
  }
}
```

An unknown task ID returns `404 Not Found`:
```json
{
  "error": {
    "code": "NOT_FOUND",
    "message": "task with ID {id} not found"
  }
}
```

### Search Tasks
```bash
curl "http://localhost:8080/api/v1/tasks/search?q=report&page_size=10"
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
  -d '{"author": "alice", "body": "Started on this"}'

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
- **Filtering & Sorting** - Query by status, assignee, tags; sort by various fields
- **UUID-based IDs** - Unique identifiers for all tasks
- **Overdue Tasks** - Paginated list of open tasks past their due date
- **Assignment Notifications** - Pluggable `Notifier` interface with a log-based default
- **Search, Stats & Comments** - Full-text search, aggregate counts and per-task comments

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
