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
│       └── notifier.go    # Notifier interface, logging default, errors
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
- `GET /api/v1/tasks/overdue` - List overdue tasks (see below)
- `GET /api/v1/tasks/:id` - Get a specific task
- `POST /api/v1/tasks` - Create a new task
- `PUT /api/v1/tasks/:id` - Update a task
- `PATCH /api/v1/tasks/:id/status` - Update task status only
- `POST /api/v1/tasks/:id/assign` - Assign a task to a user by email (see below)
- `DELETE /api/v1/tasks/:id` - Delete a task

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
`GET /api/v1/tasks/overdue` returns tasks that have a `due_date` in the past, whose status is not `completed` or `cancelled`. Tasks without a due date are never overdue. Results are sorted by `due_date` ascending, ties broken by `created_at` then `id`. It accepts the same pagination query params as `GET /api/v1/tasks` (`page_size` default 20, max 100; `page_token`); `assigned_to` and `tags` filters also apply, while `status` and `sort_order` are ignored. The route is registered before `/:id`, so `overdue` is never treated as a task ID.

```bash
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=10"
```

Response `200 OK` (same shape as the task list):
```json
{
  "tasks": [ { "id": "...", "title": "...", "status": "pending", "due_date": "2024-06-01T00:00:00Z" } ],
  "next_page_token": "10",
  "total_count": 42
}
```
`next_page_token` is omitted on the last page. Errors: `400 VALIDATION_ERROR` for invalid query params (e.g. `page_size=101`).

### Assign a Task
`POST /api/v1/tasks/:id/assign` sets the task's `assigned_to` to a bare email address (display names such as `Bob <bob@example.com>` are rejected), updates `updated_at`, and then calls the configured Notifier.

```bash
curl -X POST http://localhost:8080/api/v1/tasks/{id}/assign \
  -H "Content-Type: application/json" \
  -d '{"assigned_to": "bob@example.com"}'
```

Response `200 OK`: the updated task.
```json
{ "id": "...", "title": "...", "assigned_to": "bob@example.com", "updated_at": "2024-06-15T12:00:00Z" }
```
Errors (`ErrorResponse` format):
- `400 VALIDATION_ERROR` - malformed JSON, missing/empty `assigned_to`, or invalid email
- `404 NOT_FOUND` - task does not exist (`task with ID <id> not found`)

A notification failure is logged and does not fail the request.

**Notifier note:** the `Notifier` interface (`Notify(task models.Task, assignee string) error`) ships with a minimal logging-only default (`LogNotifier`); nothing is actually sent. The original requirements did not define an assignment-notification mechanism, so this requirement should be confirmed with the project owner. Use `TaskService.SetNotifier` to plug in a real implementation and `TaskService.SetClock` to inject a clock (used for overdue checks; handy in tests).

## Testing

```bash
go vet ./...
go test -race ./...
```

## Features

- **Gin Framework** - Fast HTTP router with middleware support
- **In-memory Storage** - Task data stored in memory (no database required)
- **Validation** - Request validation with struct tags
- **Error Handling** - Consistent error responses with status codes
- **CORS Support** - Cross-origin resource sharing enabled
- **Pagination** - Page-based pagination with tokens
- **Filtering & Sorting** - Query by status, assignee, tags; sort by various fields
- **Overdue & Assignment** - Overdue task listing and email assignment with notifier hook
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
