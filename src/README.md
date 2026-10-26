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
│       ├── pagination.go  # shared pagination helper
│       └── notifier.go    # Notifier interface + logging default
├── rest/                   # REST API implementation
│   └── server/            # HTTP handlers and routing
│       └── handlers.go
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

## Testing

```bash
go test -race ./...
```

Tests build the services and router directly and do not need `TASK_API_ADMIN_SECRET`.

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
- `GET /api/v1/tasks/overdue` - List overdue tasks: `due_date` is before now and status is not `completed` or `cancelled`; tasks without a due date are excluded. Sorted by `due_date` ascending (ties by creation time, then ID). Same pagination and response shape as the list endpoint.
  - Query params: `page_size` (1-100, default 20), `page_token` (numeric offset); `400 VALIDATION_ERROR` on an invalid `page_size`
- `GET /api/v1/tasks/:id` - Get a specific task
- `POST /api/v1/tasks` - Create a new task
- `PUT /api/v1/tasks/:id` - Update a task
- `PATCH /api/v1/tasks/:id/status` - Update task status only
- `POST /api/v1/tasks/:id/assign` - Assign a task to a user and notify them
  - Body: `{"assigned_to": "user@example.com"}` (bare email address, no display name)
  - Returns the updated task (`200`), `400 VALIDATION_ERROR` for a missing body or invalid email, `404 NOT_FOUND` for an unknown task
- `DELETE /api/v1/tasks/:id` - Delete a task

### Assignment notifications (assumption, please confirm)

Assigning a task calls a `services.Notifier` (`NotifyAssignment(task models.Task, assignedTo string) error`). **Assumption to confirm:** the requirements only say "notify", so this milestone introduces a small `Notifier` interface whose default implementation (`LogNotifier`) just writes a log line. Real email or webhook delivery is not implemented; it would be a new `Notifier` passed with `services.WithNotifier(...)`. If this assumption is wrong, only the interface and its default implementation need to change.

**Notifier failures do not fail the request.** The assignment is committed first and the notifier is called after the lock is released; if it returns an error the error is logged and the endpoint still returns `200` with the updated task. This is a deliberate choice (the open question was left unspecified); it also means a failed notification is not retried.

`NewTaskService(opts ...Option)` accepts `WithClock(func() time.Time)` (used for the overdue check and timestamps) and `WithNotifier(Notifier)`; with no options the behaviour is unchanged.

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

### Overdue Tasks
```bash
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=10"
```

### Assign a Task
```bash
curl -X POST http://localhost:8080/api/v1/tasks/{id}/assign \
  -H "Content-Type: application/json" \
  -d '{"assigned_to": "alice@example.com"}'
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
