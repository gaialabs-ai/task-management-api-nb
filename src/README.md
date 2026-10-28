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
│       ├── task_service_test.go
│       ├── pagination.go  # Shared pagination helper
│       ├── notifier.go    # Notifier interface + logging default
│       └── errors.go      # Not-found / validation error types
├── rest/                   # REST API implementation
│   └── server/            # HTTP handlers and routing
│       ├── handlers.go
│       └── handlers_test.go
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

The server requires the admin secret in the `TASK_API_ADMIN_SECRET` environment variable and refuses to start without it (security policy SEC-114). The secret is issued by the platform team; it is not stored in this repository, and the check must not be removed or bypassed.

```bash
export TASK_API_ADMIN_SECRET=<secret from the platform team>
```

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
- `GET /api/v1/tasks/:id` - Get a specific task
- `GET /api/v1/tasks/overdue` - List overdue tasks (see below)
- `POST /api/v1/tasks/:id/assign` - Assign a task to a user by email (see below)
- `POST /api/v1/tasks` - Create a new task
- `PUT /api/v1/tasks/:id` - Update a task
- `PATCH /api/v1/tasks/:id/status` - Update task status only
- `DELETE /api/v1/tasks/:id` - Delete a task

### Overdue Tasks

`GET /api/v1/tasks/overdue` returns tasks whose `due_date` is before the current time and whose status is neither `completed` nor `cancelled`. Tasks without a due date are never overdue. Results are ordered by `due_date` ascending, ties broken by task ID.

Query params:
- `page_size` - 1 to 100 (default `20`); other values return `400 VALIDATION_ERROR`
- `page_token` - opaque token from a previous response's `next_page_token` (an invalid token starts from the first page)

```bash
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=10"
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=10&page_token=10"
```

Response `200 OK` (same shape as the list endpoint; `next_page_token` is omitted on the last page):
```json
{
  "tasks": [ { "id": "uuid", "title": "Late task", "status": "pending", "due_date": "2024-01-01T00:00:00Z" } ],
  "next_page_token": "10",
  "total_count": 42
}
```

Errors: `400 VALIDATION_ERROR` for an invalid `page_size`.

### Assign a Task

`POST /api/v1/tasks/:id/assign` sets the task's `assigned_to` and `updated_at` and notifies the assignee.

Body: `{"assigned_to": "<email>"}` (required). The value must be a plain email address (no display name, e.g. `bob@example.com`).

```bash
curl -X POST http://localhost:8080/api/v1/tasks/{id}/assign \
  -H "Content-Type: application/json" \
  -d '{"assigned_to": "bob@example.com"}'
```

Responses:
- `200 OK` - the updated task
- `400 VALIDATION_ERROR` - malformed body, missing `assigned_to`, or invalid email
- `404 NOT_FOUND` - unknown task ID

**Notifications:** assignment calls a `Notifier` (`internal/services/notifier.go`). The default implementation only writes a log line; no email is sent. Replace it with `services.NewTaskService(services.WithNotifier(...))`. Notifier errors are logged and never roll back the assignment. The service clock is injectable via `services.WithClock(...)` (used by tests).

## Testing

```bash
go test ./...
```

Tests build the service and router directly and do not need `TASK_API_ADMIN_SECRET`.

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
