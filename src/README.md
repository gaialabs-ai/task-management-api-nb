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
│       └── notifier.go
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

### Overdue Tasks: `GET /api/v1/tasks/overdue`

Returns tasks whose `due_date` is in the past and whose status is neither `completed` nor `cancelled`. Tasks without a due date are never overdue. Results are sorted by due date ascending (oldest first), with the task ID as a tie-breaker. The response has the same shape as `GET /api/v1/tasks` (`tasks`, `next_page_token`, `total_count`).

Query parameters (same pagination as the list endpoint):
- `page_size` - 1 to 100, default 20 (values over 100 are rejected with 400)
- `page_token` - opaque token (the numeric offset) taken from `next_page_token` of the previous page

Other list filters (`status`, `assigned_to`, `tags`, `sort_order`) are accepted by the query binding but ignored by this endpoint.

Status codes: `200 OK`; `400 Bad Request` (`VALIDATION_ERROR`) for an invalid query such as `page_size=0` or `page_size=abc`.

### Assign a Task: `POST /api/v1/tasks/:id/assign`

Request body:
```json
{"assigned_to": "alice@example.com"}
```
`assigned_to` is required and must be a valid email address. On success the task's `assigned_to` and `updated_at` are updated, the assignee is notified, and the updated task is returned.

Status codes:
- `200 OK` - task assigned; body is the updated task
- `400 Bad Request` (`VALIDATION_ERROR`) - missing/invalid email or malformed JSON body
- `404 Not Found` (`NOT_FOUND`) - no task with that ID

Notification: assignment calls a `Notifier` (`services.Notifier`, method `NotifyAssignment(task, assignee)`) after the update, outside the service lock. The default implementation only writes a log line. A notification failure is logged and does not undo the assignment or change the response. A custom notifier and clock can be injected with `services.NewTaskService(services.WithNotifier(n), services.WithClock(fn))`; `NewTaskService()` with no options behaves as before.

Known gaps and notes:
- The specification has no "invalid state" rule for assignment, so only 400 and 404 are implemented. Completed and cancelled tasks can be assigned, and re-assigning an already assigned task simply overwrites it.
- Notifier discrepancy: no assignment notification existed before this change (the only existing way to set `assigned_to` is `PUT /tasks/:id`, which does not notify). The `Notifier` is new and is only invoked by the assign endpoint; the default implementation does not deliver anything externally.

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

### List Overdue Tasks
```bash
curl "http://localhost:8080/api/v1/tasks/overdue"

# Paginated
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=10&page_token=10"
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
