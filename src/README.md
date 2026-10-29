# Task Management REST API - Go Implementation

A task management REST API built with Go and the Gin framework. This implementation provides CRUD operations for tasks with support for pagination, filtering, status management, search, statistics, overdue listing, assignment notifications and comments.

## Directory Structure

```
.
├── cmd/
│   └── rest-server/              # REST server entry point (license-gated)
│       └── main.go
├── internal/                     # Private application code
│   ├── models/                   # Data models and types
│   │   ├── comment.go            # Comment model and request/response types
│   │   └── task.go               # Task model, queries, requests, responses
│   ├── notifications/            # Assignment notification extension point
│   │   └── notifier.go           # Notifier interface and default LogNotifier
│   └── services/                 # Business logic (in-memory storage)
│       ├── comment_service.go
│       ├── task_service.go
│       └── task_service_test.go  # Service unit tests
├── rest/                         # REST API implementation
│   └── server/                   # HTTP handlers and routing
│       ├── handlers.go
│       └── handlers_test.go      # HTTP tests (httptest against the real router)
├── go.mod                        # Go module definition
├── go.sum                        # Dependency checksums
└── README.md
```

Test files follow the Go convention `*_test.go` and live next to the code they test.

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
- `GET /api/v1/tasks/search` - Full-text search over titles, descriptions and tags
  - Query params: `q` (required), `page_size` (default `20`, `1`-`100`)
- `GET /api/v1/tasks/stats` - Aggregate task counts by status and priority
- `GET /api/v1/tasks/overdue` - List overdue tasks (see [Overdue Tasks](#overdue-tasks))
  - Query params: `page_size`, `page_token`
- `GET /api/v1/tasks/:id` - Get a specific task
- `POST /api/v1/tasks` - Create a new task
- `PUT /api/v1/tasks/:id` - Update a task
- `PATCH /api/v1/tasks/:id/status` - Update task status only
- `POST /api/v1/tasks/:id/assign` - Assign a task by email and send a notification (see [Assign a Task](#assign-a-task))
- `DELETE /api/v1/tasks/:id` - Delete a task

### Comments
- `GET /api/v1/tasks/:id/comments` - List a task's comments (oldest first)
- `POST /api/v1/tasks/:id/comments` - Add a comment to a task
- `DELETE /api/v1/tasks/:id/comments/:commentId` - Delete a comment from a task

### Search Tasks

`GET /api/v1/tasks/search?q=<query>` performs a case-insensitive substring match on task titles, descriptions and tags. Results are ordered by relevance (title matches first, then description, then tags) and limited to `page_size` results; there is no `page_token`.

| Param | Required | Description |
|-------|----------|-------------|
| `q` | Yes | Search text (at least 1 character) |
| `page_size` | No | Maximum results to return. Default `20`, allowed `1`-`100` |

Response (`200 OK`):
```json
{
  "tasks": [ /* Task objects */ ],
  "total_count": 3,
  "query": "go"
}
```

`total_count` is the number of all matches, not just the returned page. A missing `q` (or an invalid `page_size`) returns `400 VALIDATION_ERROR` with the message `Query parameter 'q' is required`.

### Task Statistics

`GET /api/v1/tasks/stats` returns counts across all tasks. Every status and priority key is always present (zero when empty).

```json
{
  "total": 2,
  "by_status": {"pending": 1, "in_progress": 1, "completed": 0, "cancelled": 0, "on_hold": 0},
  "by_priority": {"low": 0, "medium": 1, "high": 1, "critical": 0}
}
```

### Overdue Tasks

`GET /api/v1/tasks/overdue` returns tasks that are past their due date and still open.

**Overdue rule:** a task is overdue when
- `due_date` is set, **and**
- `due_date` is strictly before the current time (a task due exactly now is not overdue), **and**
- `status` is not `completed` or `cancelled` (so `pending`, `in_progress` and `on_hold` tasks qualify).

Results are sorted by `due_date` ascending (oldest first), with `id` as the tiebreaker for equal due dates.

| Param | Required | Description |
|-------|----------|-------------|
| `page_size` | No | Tasks per page. Default `20`, allowed `1`-`100`. Any other value (e.g. `0`, `101`, non-numeric) returns `400 VALIDATION_ERROR` |
| `page_token` | No | Offset into the sorted result, taken from a previous response's `next_page_token`. Omit for the first page |

Response (`200 OK`, same shape as `GET /api/v1/tasks`):
```json
{
  "tasks": [ /* Task objects */ ],
  "next_page_token": "20",
  "total_count": 42
}
```

- `next_page_token` is present only when more results remain; it is omitted on the last page.
- `total_count` is the total number of overdue tasks across all pages.
- When nothing is overdue (or `page_token` is past the end), the response is still `200 OK` with `total_count` (`0` when nothing is overdue), no `next_page_token`, and `tasks` serialized as `null`.

Errors:
- `400 VALIDATION_ERROR` - `page_size` outside `1`-`100` or not a number

### Assign a Task

`POST /api/v1/tasks/:id/assign` sets the task's assignee and sends an assignment notification.

Request body:
```json
{"assigned_to": "user@example.com"}
```

- `assigned_to` is required and must be a valid email address. Surrounding whitespace is trimmed before it is stored.

Response (`200 OK`): the updated Task, with `assigned_to` set to the email and `updated_at` refreshed.

**Notification:** after the assignment is saved, the service calls the `Notifier` interface (`internal/notifications`). The default `LogNotifier` writes a log line in this format:

```
assignment notification: task <id> "<title>" assigned to <email>
```

If the notifier returns an error, the error is logged and the assignment is **not** rolled back; the request still returns `200 OK`. A different notification implementation can be plugged in behind the `Notifier` interface without changing the handlers.

Errors:
- `400 VALIDATION_ERROR` - missing `assigned_to`, invalid email, or malformed JSON
- `404 NOT_FOUND` - no task with the given `id`

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

## Comment Model

### Comment Fields
```json
{
  "id": "uuid",
  "task_id": "uuid",
  "author": "string (required, 1-100 chars)",
  "body": "string (required, 1-5000 chars)",
  "created_at": "timestamp",
  "updated_at": "timestamp"
}
```

`POST /api/v1/tasks/:id/comments` accepts `{"author": "...", "body": "..."}` and returns `201 Created` with the new Comment. `GET /api/v1/tasks/:id/comments` returns `{"comments": [...], "total": <n>}`. `DELETE /api/v1/tasks/:id/comments/:commentId` returns `204 No Content`. All three return `404 NOT_FOUND` for an unknown task ID (and `DELETE` also for an unknown comment ID); `POST` returns `400 VALIDATION_ERROR` for an invalid body.

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
# First page (default page size 20)
curl http://localhost:8080/api/v1/tasks/overdue

# Smaller pages
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=10"

# Next page, using next_page_token from the previous response
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=10&page_token=10"

# Invalid page size -> 400 VALIDATION_ERROR
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=0"
```

### Assign a Task
```bash
curl -X POST http://localhost:8080/api/v1/tasks/{id}/assign \
  -H "Content-Type: application/json" \
  -d '{"assigned_to": "user@example.com"}'
```

### Search Tasks
```bash
curl "http://localhost:8080/api/v1/tasks/search?q=go&page_size=5"
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
- **Search & Stats** - Full-text task search and aggregate counts
- **Overdue Tasks** - Paginated list of open tasks past their due date
- **Assignment Notifications** - Email-validated assignment with a pluggable `Notifier` (log-based by default)
- **Comments** - Notes attached to tasks

## Testing

Run all commands from `src/` (where `go.mod` lives):

```bash
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

Verification runs through in-process `go test`: the HTTP tests build a `gin.New()` engine, register the real routes with `server.NewHandler(svc).RegisterRoutes(r)`, and send requests (including `GET /health`) through `httptest`. This exercises the same router and handlers that `main.go` wires up. The server binary itself is not used for verification because it only starts on the licensed workstation (see [Required Configuration](#required-configuration)); do not create a license file or stub the check to run it elsewhere.

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
