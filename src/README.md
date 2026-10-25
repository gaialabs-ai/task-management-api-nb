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
│   │   └── task.go
│   └── services/          # Business logic
│       ├── task_service.go
│       ├── comment_service.go
│       └── task_service_test.go   # Service unit tests (overdue, assign, pagination)
├── rest/                   # REST API implementation
│   └── server/            # HTTP handlers and routing
│       ├── handlers.go
│       └── handlers_test.go       # HTTP-level tests (pagination, overdue, assign)
├── go.mod                  # Go module definition
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

## API Endpoints

### Health Check
- `GET /health` - Server health status

### Tasks
- `GET /api/v1/tasks` - List all tasks
  - Query params: `page_size`, `page_token`, `status`, `assigned_to`, `tags`, `sort_order`
  - Pagination notes: with no `sort_order`, results are ordered by `created_at` ascending (ties by `id`) so `page_token` offsets are stable across requests. Other `sort_order` values keep this order for ties. A negative or non-numeric `page_token` is treated as the start of the list (previously a negative token caused a server error).
- `GET /api/v1/tasks/search` - Search tasks by title, description, and tags (`q`, `page_size`)
- `GET /api/v1/tasks/stats` - Task counts by status and priority
- `GET /api/v1/tasks/overdue` - List overdue tasks (see [Overdue Tasks](#overdue-tasks))
  - Query params: `page_size`, `page_token`
- `GET /api/v1/tasks/:id` - Get a specific task
- `POST /api/v1/tasks` - Create a new task
- `PUT /api/v1/tasks/:id` - Update a task
- `POST /api/v1/tasks/:id/assign` - Assign a task to an email address (see [Assigning Tasks](#assigning-tasks))
- `PATCH /api/v1/tasks/:id/status` - Update task status only
- `DELETE /api/v1/tasks/:id` - Delete a task
- `GET /api/v1/tasks/:id/comments` - List comments on a task
- `POST /api/v1/tasks/:id/comments` - Add a comment to a task
- `DELETE /api/v1/tasks/:id/comments/:commentId` - Delete a comment

## Overdue Tasks

`GET /api/v1/tasks/overdue` returns tasks that are past due.

**Definition.** A task is overdue when both of the following hold:
- it has a `due_date` (tasks without one are never overdue) that is strictly before the current time (a task due exactly "now" is not overdue); and
- its status is neither `completed` nor `cancelled`.

**Ordering.** Results are sorted by `due_date` ascending (most overdue first). Ties are broken deterministically by `created_at` ascending, then by `id`, so pages are stable.

**Pagination.** Uses the same `page_size` / `page_token` offset logic as `GET /api/v1/tasks`:
- `page_size` defaults to 20 when omitted. The handler binds the query with the same rules as `GET /api/v1/tasks`, so a value outside 1-100 (or a non-integer) is rejected with `400 VALIDATION_ERROR`. The service additionally caps the page size at 100.
- `page_token` is the opaque offset returned as `next_page_token` by the previous page. An invalid token is treated as the start; a token past the end yields an empty page.
- The response contains `tasks`, `next_page_token` (omitted on the last page), and `total_count` (the total number of overdue tasks across all pages, not just this page). `tasks` is always a JSON array, `[]` when nothing is overdue.

**Other query params.** The handler binds the same query struct as `GET /api/v1/tasks` (`page_size`, `page_token`, `status`, `assigned_to`, `tags`, `sort_order`), but only `page_size` and `page_token` affect the overdue result. `status`, `assigned_to`, `tags`, and `sort_order` are accepted but ignored; ordering is always as described above.

**Routing.** The route is registered before `GET /api/v1/tasks/:id`, so `overdue` is matched as this endpoint and not treated as a task ID.

```bash
# First page of overdue tasks
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=2"

# Next page (use next_page_token from the previous response)
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=2&page_token=2"
```

Example response:
```json
{
  "tasks": [
    {
      "id": "3f0c2a4e-8b1d-4c7e-9a52-6d1f0e7b9c11",
      "title": "File quarterly report",
      "description": "",
      "status": "pending",
      "priority": "medium",
      "tags": [],
      "created_by": "system",
      "created_at": "2025-05-20T09:00:00Z",
      "updated_at": "2025-05-20T09:00:00Z",
      "due_date": "2025-05-27T12:00:00Z"
    },
    {
      "id": "b7e1d9c2-4a63-4f08-8d35-2c9a1e5f7a40",
      "title": "Renew certificates",
      "description": "",
      "status": "in_progress",
      "priority": "high",
      "tags": ["ops"],
      "created_by": "system",
      "created_at": "2025-05-22T10:30:00Z",
      "updated_at": "2025-05-30T08:15:00Z",
      "due_date": "2025-05-30T12:00:00Z"
    }
  ],
  "next_page_token": "2",
  "total_count": 3
}
```

## Assigning Tasks

`POST /api/v1/tasks/:id/assign` assigns a task to a person identified by email address. It sets `assigned_to` and refreshes `updated_at`.

**Request body**
```json
{"assigned_to": "user@example.com"}
```

**Validation.** `assigned_to` is required. Surrounding whitespace is trimmed, and the value must be a bare email address as parsed by Go's `net/mail` (`mail.ParseAddress`). Display-name forms such as `Name <a@b.com>` are rejected, as are values that are not email addresses. The stored value is the trimmed address. The email is validated before the task is looked up, so an invalid email yields `400` even for an unknown task ID.

**Responses**
- `200 OK` - the updated task (full task JSON).
- `400 Bad Request` with `VALIDATION_ERROR` - malformed JSON, missing or empty `assigned_to`, or an invalid email address.
- `404 Not Found` with `NOT_FOUND` - no task with that ID.

Errors use the standard [error response format](#error-response-format).

```bash
curl -X POST http://localhost:8080/api/v1/tasks/{id}/assign \
  -H "Content-Type: application/json" \
  -d '{"assigned_to": "user@example.com"}'
```

Example success response (`200`):
```json
{
  "id": "3f0c2a4e-8b1d-4c7e-9a52-6d1f0e7b9c11",
  "title": "File quarterly report",
  "description": "",
  "status": "pending",
  "priority": "medium",
  "tags": [],
  "created_by": "system",
  "assigned_to": "user@example.com",
  "created_at": "2025-05-20T09:00:00Z",
  "updated_at": "2025-06-01T12:00:00Z",
  "due_date": "2025-05-27T12:00:00Z"
}
```

Example validation error (`400`):
```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "invalid email: \"Bob <b@x.com>\""
  }
}
```

Example not-found error (`404`):
```json
{
  "error": {
    "code": "NOT_FOUND",
    "message": "task with ID missing not found"
  }
}
```

**Assignment notification.** After a successful assignment the service calls a `Notifier` (interface in `internal/services/task_service.go`):

```go
type Notifier interface {
    NotifyAssignment(task models.Task, assignee string) error
}
```

The default implementation, `LogNotifier`, writes a line to the standard logger (`notification: task <id> ("<title>") assigned to <email>`). The notifier is called only on successful assignments, after the internal lock is released. If it returns an error, the error is logged and the request still succeeds with `200`. `TaskService.SetNotifier` swaps the implementation (`nil` disables notifications), and `TaskService.SetClock` swaps the time source (used by tests for deterministic overdue and `updated_at` behavior).

> **Note - to be confirmed with the project owner:** no assignment notification existed in this codebase before the assign endpoint was added. The `Notifier` interface and the logging-only `LogNotifier` were introduced as a minimal way to satisfy the "notify on assignment" requirement. They do not send email or any other external message. Whether this is sufficient, or a real delivery channel (email, webhook, etc.) is required, has not been decided and should be confirmed with the project owner.

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

## Testing

Run from the `src/` directory:

```bash
go vet ./...
go test -race ./...
```

The tests do not need the workstation license. They build the router directly with `server.NewHandler` and `RegisterRoutes` (using `httptest`) instead of going through `cmd/rest-server/main.go`, which is where the license check runs. The license check itself is unchanged; see [Required Configuration](#required-configuration).

Time-dependent behavior (overdue) and notifications are made deterministic in tests with `TaskService.SetClock` and `TaskService.SetNotifier`.

## Features

- **Gin Framework** - Fast HTTP router with middleware support
- **In-memory Storage** - Task data stored in memory (no database required)
- **Validation** - Request validation with struct tags
- **Error Handling** - Consistent error responses with status codes
- **CORS Support** - Cross-origin resource sharing enabled
- **Pagination** - Page-based pagination with tokens
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
