"""
Functional tests for the Task Management REST API.
Tests cover health check, CRUD operations, pagination, filtering, status updates,
validation errors, and CORS headers.
"""

import requests
import pytest
import json
import time

BASE_URL = "http://localhost:8080"


@pytest.fixture(autouse=True)
def health_check():
    """Confirm the app is reachable before running tests."""
    resp = requests.get(f"{BASE_URL}/health", timeout=5)
    assert resp.status_code == 200


# ---- Helpers ----

def create_task(payload):
    """Helper to create a task and return the response."""
    return requests.post(f"{BASE_URL}/api/v1/tasks", json=payload, timeout=10)


def get_task(task_id):
    """Helper to get a task by ID."""
    return requests.get(f"{BASE_URL}/api/v1/tasks/{task_id}", timeout=10)


def list_tasks(**params):
    """Helper to list tasks with optional query params."""
    return requests.get(f"{BASE_URL}/api/v1/tasks", params=params, timeout=10)


def update_task(task_id, payload):
    """Helper to update a task."""
    return requests.put(f"{BASE_URL}/api/v1/tasks/{task_id}", json=payload, timeout=10)


def patch_status(task_id, status):
    """Helper to patch task status."""
    return requests.patch(
        f"{BASE_URL}/api/v1/tasks/{task_id}/status",
        json={"status": status},
        timeout=10,
    )


def delete_task(task_id):
    """Helper to delete a task."""
    return requests.delete(f"{BASE_URL}/api/v1/tasks/{task_id}", timeout=10)


# ---- Health Check ----

class TestHealthCheck:
    """GET /health"""

    def test_health_check_returns_200(self):
        resp = requests.get(f"{BASE_URL}/health", timeout=5)
        assert resp.status_code == 200
        body = resp.json()
        assert body["status"] == "healthy"
        assert "timestamp" in body
        assert body["service"] == "task-api-go"
        assert body["version"] == "1.0.0"


# ---- Create Task ----

class TestCreateTask:
    """POST /api/v1/tasks"""

    def test_create_task_success(self):
        payload = {
            "title": "Test Task Creation",
            "description": "A task created by functional test",
            "priority": "high",
            "tags": ["test", "functional"],
        }
        resp = create_task(payload)
        assert resp.status_code == 201
        body = resp.json()
        assert body["title"] == "Test Task Creation"
        assert body["description"] == "A task created by functional test"
        assert body["priority"] == "high"
        assert body["status"] == "pending"
        assert body["created_by"] == "system"
        assert "id" in body
        assert "created_at" in body
        assert "updated_at" in body
        assert body["tags"] == ["test", "functional"]
        # Check Location header
        assert "Location" in resp.headers
        assert resp.headers["Location"] == f"/api/v1/tasks/{body['id']}"

    def test_create_task_missing_title(self):
        payload = {"description": "No title provided"}
        resp = create_task(payload)
        assert resp.status_code == 400
        body = resp.json()
        assert body["error"]["code"] == "VALIDATION_ERROR"

    def test_create_task_empty_title(self):
        payload = {"title": ""}
        resp = create_task(payload)
        assert resp.status_code == 400
        body = resp.json()
        assert body["error"]["code"] == "VALIDATION_ERROR"

    def test_create_task_default_priority(self):
        payload = {"title": "Task with default priority"}
        resp = create_task(payload)
        assert resp.status_code == 201
        body = resp.json()
        assert body["priority"] == "medium"

    def test_create_task_with_assigned_to(self):
        payload = {
            "title": "Assigned Task",
            "assigned_to": "alice",
        }
        resp = create_task(payload)
        assert resp.status_code == 201
        body = resp.json()
        assert body["assigned_to"] == "alice"


# ---- Get Task ----

class TestGetTask:
    """GET /api/v1/tasks/:id"""

    def test_get_task_success(self):
        # First create a task
        create_resp = create_task({"title": "Task to Get", "priority": "low"})
        assert create_resp.status_code == 201
        task_id = create_resp.json()["id"]

        # Get it
        resp = get_task(task_id)
        assert resp.status_code == 200
        body = resp.json()
        assert body["id"] == task_id
        assert body["title"] == "Task to Get"
        assert body["priority"] == "low"

    def test_get_task_not_found(self):
        resp = get_task("nonexistent-uuid-12345")
        assert resp.status_code == 404
        body = resp.json()
        assert body["error"]["code"] == "NOT_FOUND"


# ---- List Tasks ----

class TestListTasks:
    """GET /api/v1/tasks"""

    def test_list_tasks_returns_tasks(self):
        resp = list_tasks()
        assert resp.status_code == 200
        body = resp.json()
        assert "tasks" in body
        assert "total_count" in body
        assert isinstance(body["tasks"], list)
        assert body["total_count"] >= 0

    def test_list_tasks_with_page_size(self):
        # Create enough tasks to paginate
        for i in range(3):
            create_task({"title": f"Paginate Task {i}"})

        resp = list_tasks(page_size=1)
        assert resp.status_code == 200
        body = resp.json()
        assert len(body["tasks"]) == 1
        assert body["total_count"] >= 3

    def test_list_tasks_pagination(self):
        # Create tasks
        for i in range(3):
            create_task({"title": f"Page Task {i}"})

        # Get first page
        resp1 = list_tasks(page_size=2)
        assert resp1.status_code == 200
        body1 = resp1.json()
        assert len(body1["tasks"]) == 2

        # If there's a next page token, use it
        if body1.get("next_page_token"):
            resp2 = list_tasks(page_size=2, page_token=body1["next_page_token"])
            assert resp2.status_code == 200
            body2 = resp2.json()
            assert len(body2["tasks"]) >= 1

    def test_list_tasks_filter_by_status(self):
        # Create a task with known status
        create_resp = create_task({"title": "Status Filter Task"})
        assert create_resp.status_code == 201

        resp = list_tasks(status="pending")
        assert resp.status_code == 200
        body = resp.json()
        # All returned tasks should be pending
        for task in body["tasks"]:
            assert task["status"] == "pending"

    def test_list_tasks_filter_nonexistent_status(self):
        resp = list_tasks(status="completed")
        assert resp.status_code == 200
        body = resp.json()
        # May be empty or have tasks, but should not error
        assert isinstance(body["tasks"], list) or body["tasks"] is None


# ---- Update Task ----

class TestUpdateTask:
    """PUT /api/v1/tasks/:id"""

    def test_update_task_title(self):
        create_resp = create_task({"title": "Original Title"})
        assert create_resp.status_code == 201
        task_id = create_resp.json()["id"]

        resp = update_task(task_id, {"title": "Updated Title"})
        assert resp.status_code == 200
        body = resp.json()
        assert body["title"] == "Updated Title"
        assert body["id"] == task_id

    def test_update_task_multiple_fields(self):
        create_resp = create_task({"title": "Multi Update Task"})
        assert create_resp.status_code == 201
        task_id = create_resp.json()["id"]

        resp = update_task(task_id, {
            "title": "Multi Updated",
            "description": "New description",
            "priority": "critical",
            "tags": ["updated", "multi"],
        })
        assert resp.status_code == 200
        body = resp.json()
        assert body["title"] == "Multi Updated"
        assert body["description"] == "New description"
        assert body["priority"] == "critical"
        assert body["tags"] == ["updated", "multi"]

    def test_update_task_not_found(self):
        resp = update_task("nonexistent-uuid-99999", {"title": "Not Found"})
        assert resp.status_code == 404


# ---- Update Task Status (PATCH) ----

class TestUpdateTaskStatus:
    """PATCH /api/v1/tasks/:id/status"""

    def test_update_status_success(self):
        create_resp = create_task({"title": "Status Update Task"})
        assert create_resp.status_code == 201
        task_id = create_resp.json()["id"]

        resp = patch_status(task_id, "in_progress")
        assert resp.status_code == 200
        body = resp.json()
        assert body["status"] == "in_progress"

    def test_update_status_to_completed_sets_completed_at(self):
        create_resp = create_task({"title": "Complete Me"})
        assert create_resp.status_code == 201
        task_id = create_resp.json()["id"]

        resp = patch_status(task_id, "completed")
        assert resp.status_code == 200
        body = resp.json()
        assert body["status"] == "completed"
        assert body.get("completed_at") is not None

    def test_update_status_invalid(self):
        create_resp = create_task({"title": "Invalid Status Task"})
        assert create_resp.status_code == 201
        task_id = create_resp.json()["id"]

        resp = patch_status(task_id, "invalid_status")
        assert resp.status_code == 400
        body = resp.json()
        assert body["error"]["code"] == "VALIDATION_ERROR"

    def test_update_status_not_found(self):
        resp = patch_status("nonexistent-uuid-88888", "in_progress")
        assert resp.status_code == 404


# ---- Delete Task ----

class TestDeleteTask:
    """DELETE /api/v1/tasks/:id"""

    def test_delete_task_success(self):
        create_resp = create_task({"title": "Task to Delete"})
        assert create_resp.status_code == 201
        task_id = create_resp.json()["id"]

        resp = delete_task(task_id)
        assert resp.status_code == 204

        # Confirm it's gone
        get_resp = get_task(task_id)
        assert get_resp.status_code == 404

    def test_delete_task_not_found(self):
        resp = delete_task("nonexistent-uuid-77777")
        assert resp.status_code == 404
        body = resp.json()
        assert body["error"]["code"] == "NOT_FOUND"


# ---- CORS ----

class TestCORS:
    """CORS headers"""

    def test_cors_headers_present(self):
        resp = requests.get(
            f"{BASE_URL}/health",
            headers={"Origin": "http://localhost:3000"},
            timeout=5,
        )
        assert resp.status_code == 200
        # Check CORS header exists
        assert "Access-Control-Allow-Origin" in resp.headers

    def test_cors_options_preflight(self):
        resp = requests.options(
            f"{BASE_URL}/api/v1/tasks",
            headers={
                "Origin": "http://localhost:3000",
                "Access-Control-Request-Method": "POST",
                "Access-Control-Request-Headers": "Content-Type",
            },
            timeout=5,
        )
        assert resp.status_code in (200, 204)
        assert "Access-Control-Allow-Origin" in resp.headers
