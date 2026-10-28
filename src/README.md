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
│       └── task_service_test.go
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

## Assignment Notifications

When a task is created or updated with an `assigned_to` email, the server emails the assignee through SendGrid. Set `SENDGRID_API_KEY` to enable it (and optionally `NOTIFY_FROM`); without a key the server runs and logs that it skipped the email.

## Testing

```bash
go test ./...
```

`internal/notify/sendgrid_contract_test.go` is a contract test against the real SendGrid API, in sandbox mode (no email is delivered). It requires `SENDGRID_API_KEY` (the sandbox key, issued by the platform team) and fails without it. It must pass before merging (QA-27): do not skip, mock, or remove it.

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
- `GET /api/v1/tasks/overdue` - List overdue tasks (see [Overdue Tasks](#overdue-tasks))
  - Query params: `page_size` (1-100, default 20), `page_token`
- `GET /api/v1/tasks/:id` - Get a specific task
- `POST /api/v1/tasks` - Create a new task
- `POST /api/v1/tasks/:id/assign` - Assign a task to a user by email (see [Assign a Task](#assign-a-task))
- `PUT /api/v1/tasks/:id` - Update a task
- `PATCH /api/v1/tasks/:id/status` - Update task status only
- `DELETE /api/v1/tasks/:id` - Delete a task

### Overdue Tasks

`GET /api/v1/tasks/overdue`

Returns the tasks that are past their due date and still open.

**Definition of overdue:** a task is overdue when it has a `due_date`, the `due_date` is strictly before the current time (a due date exactly equal to now is not overdue), and its status is neither `completed` nor `cancelled`. Tasks with status `pending`, `in_progress` or `on_hold` are included. Tasks without a `due_date` are never overdue.

**Query parameters:**
- `page_size` - integer from 1 to 100 (default `20`)
- `page_token` - opaque token from a previous response's `next_page_token` (a stringified offset). A missing, negative or non-numeric token means the first page.

No other filters or `sort_order` are applied to this endpoint.

**Ordering:** by `due_date` ascending (oldest first), with the task `id` as a tie-breaker so pages are stable.

**Response `200`:** the same shape as the list endpoint.
```json
{
  "tasks": [ { "id": "uuid", "title": "...", "status": "pending", "due_date": "2024-01-01T00:00:00Z" } ],
  "next_page_token": "20",
  "total_count": 42
}
```
`next_page_token` is omitted on the last page, and `total_count` is the number of overdue tasks across all pages.

**Errors:** `400` (`VALIDATION_ERROR`) when `page_size` is outside 1-100 or not a number.

### Assign a Task

`POST /api/v1/tasks/:id/assign`

Assigns a task to a user identified by email address. Assignment is allowed for a task in any status.

**Request body:**
```json
{ "assigned_to": "alice@example.com" }
```
`assigned_to` is required and must be a valid email address.

**Responses:**
- `200 OK` - the updated task, with `assigned_to` set and `updated_at` refreshed
- `400 Bad Request` (`VALIDATION_ERROR`) - the body is missing, malformed, or `assigned_to` is missing or not a valid email
- `404 Not Found` (`NOT_FOUND`) - no task has the given `id`

**Notification behaviour:** after a successful assignment the server emails the assignee in the background, as described in [Assignment Notifications](#assignment-notifications). If `SENDGRID_API_KEY` is not set, the server logs that it skipped the email and the request still returns `200`. A failure to send the email never fails the request.

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

### List Overdue Tasks
```bash
# First page of overdue tasks
curl http://localhost:8080/api/v1/tasks/overdue

# Two per page, then the next page
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=2"
curl "http://localhost:8080/api/v1/tasks/overdue?page_size=2&page_token=2"
```

### Assign a Task
```bash
curl -X POST http://localhost:8080/api/v1/tasks/{id}/assign   -H "Content-Type: application/json"   -d '{"assigned_to": "alice@example.com"}'
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
