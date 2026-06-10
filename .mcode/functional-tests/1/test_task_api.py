import requests
import pytest
import json

BASE_URL = "http://localhost:8080"


@pytest.fixture(autouse=True)
def health_check():
    """Confirm the app is reachable before running tests."""
    resp = requests.get(f"{BASE_URL}/health", timeout=5)
    assert resp.status_code == 200


class TestHealthCheck:
    """GET /health -- health check endpoint."""

    def test_health_returns_200(self):
        resp = requests.get(f"{BASE_URL}/health", timeout=5)
        assert resp.status_code == 200
        body = resp.json()
        assert body["status"] == "healthy"
        assert body["service"] == "task-api-go"
        assert body["version"] == "1.0.0"
        assert "timestamp" in body


class TestListTasks:
    """GET /api/v1/tasks -- list tasks."""

    def test_list_tasks_returns_sample_data(self):
        resp = requests.get(f"{BASE_URL}/api/v1/tasks", timeout=10)
        assert resp.status_code == 200
        body = resp.json()
        assert "tasks" in body
        assert "total_count" in body
        assert body["total_count"] >= 2
        titles = [t["title"] for t in body["tasks"]]
        assert "Implement Go REST API" in titles
        assert "Add gRPC support" in titles

    def test_list_tasks_pagination(self):
        resp = requests.get(f"{BASE_URL}/api/v1/tasks?page_size=1", timeout=10)
        assert resp.status_code == 200
        body = resp.json()
        assert len(body["tasks"]) == 1
        assert body["total_count"] >= 2
        assert "next_page_token" in body
        token = body["next_page_token"]
        assert token != ""
        # Follow next page
        resp2 = requests.get(
            f"{BASE_URL}/api/v1/tasks?page_size=1&page_token={token}", timeout=10
        )
        assert resp2.status_code == 200
        body2 = resp2.json()
        assert len(body2["tasks"]) == 1
        # The two pages should have different tasks
        assert body["tasks"][0]["id"] != body2["tasks"][0]["id"]

    def test_list_tasks_filter_by_status(self):
        resp = requests.get(
            f"{BASE_URL}/api/v1/tasks?status=pending", timeout=10
        )
        assert resp.status_code == 200
        body = resp.json()
        for task in body["tasks"]:
            assert task["status"] == "pending"

    def test_list_tasks_filter_by_tags(self):
        resp = requests.get(f"{BASE_URL}/api/v1/tasks?tags=go", timeout=10)
        assert resp.status_code == 200
        body = resp.json()
        for task in body["tasks"]:
            assert "go" in task["tags"]

    def test_list_tasks_sort_by_priority_desc(self):
        resp = requests.get(
            f"{BASE_URL}/api/v1/tasks?sort_order=priority_desc", timeout=10
        )
        assert resp.status_code == 200
        body = resp.json()
        if len(body["tasks"]) >= 2:
            priority_order = {"critical": 4, "high": 3, "medium": 2, "low": 1}
            for i in range(len(body["tasks"]) - 1):
                p1 = priority_order.get(body["tasks"][i]["priority"], 0)
                p2 = priority_order.get(body["tasks"][i + 1]["priority"], 0)
                assert p1 >= p2


class TestGetTask:
    """GET /api/v1/tasks/:id -- get a single task."""

    def test_get_existing_task(self):
        # First list tasks to get an ID
        resp = requests.get(f"{BASE_URL}/api/v1/tasks", timeout=10)
        tasks = resp.json()["tasks"]
        task_id = tasks[0]["id"]
        resp2 = requests.get(f"{BASE_URL}/api/v1/tasks/{task_id}", timeout=10)
        assert resp2.status_code == 200
        body = resp2.json()
        assert body["id"] == task_id
        assert "title" in body
        assert "status" in body

    def test_get_nonexistent_task(self):
        resp = requests.get(
            f"{BASE_URL}/api/v1/tasks/nonexistent-id-12345", timeout=10
        )
        assert resp.status_code == 404
        body = resp.json()
        assert body["error"]["code"] == "NOT_FOUND"


class TestCreateTask:
    """POST /api/v1/tasks -- create a new task."""

    def test_create_task_success(self):
        payload = {
            "title": "Test task creation",
            "description": "A test task",
            "priority": "high",
            "tags": ["test", "functional"],
        }
        resp = requests.post(
            f"{BASE_URL}/api/v1/tasks", json=payload, timeout=10
        )
        assert resp.status_code == 201
        body = resp.json()
        assert body["title"] == "Test task creation"
        assert body["description"] == "A test task"
        assert body["priority"] == "high"
        assert body["status"] == "pending"
        assert "id" in body
        assert "created_at" in body
        assert body["tags"] == ["test", "functional"]

    def test_create_task_missing_title(self):
        payload = {"description": "No title provided"}
        resp = requests.post(
            f"{BASE_URL}/api/v1/tasks", json=payload, timeout=10
        )
        assert resp.status_code == 400
        body = resp.json()
        assert body["error"]["code"] == "VALIDATION_ERROR"

    def test_create_task_empty_body(self):
        resp = requests.post(
            f"{BASE_URL}/api/v1/tasks",
            data="{}",
            headers={"Content-Type": "application/json"},
            timeout=10,
        )
        assert resp.status_code == 400

    def test_create_task_default_priority(self):
        payload = {"title": "Task with default priority"}
        resp = requests.post(
            f"{BASE_URL}/api/v1/tasks", json=payload, timeout=10
        )
        assert resp.status_code == 201
        body = resp.json()
        assert body["priority"] == "medium"

    def test_create_task_invalid_priority(self):
        payload = {"title": "Task with bad priority", "priority": "urgent"}
        resp = requests.post(
            f"{BASE_URL}/api/v1/tasks", json=payload, timeout=10
        )
        assert resp.status_code == 400
        body = resp.json()
        assert body["error"]["code"] == "VALIDATION_ERROR"

    def test_create_task_with_assigned_to(self):
        payload = {
            "title": "Assigned task",
            "assigned_to": "user@example.com",
        }
        resp = requests.post(
            f"{BASE_URL}/api/v1/tasks", json=payload, timeout=10
        )
        assert resp.status_code == 201
        body = resp.json()
        assert body["assigned_to"] == "user@example.com"


class TestUpdateTask:
    """PUT /api/v1/tasks/:id -- update a task."""

    def test_update_task_title(self):
        # Create a task first
        create_resp = requests.post(
            f"{BASE_URL}/api/v1/tasks",
            json={"title": "Original title"},
            timeout=10,
        )
        task_id = create_resp.json()["id"]
        # Update it
        resp = requests.put(
            f"{BASE_URL}/api/v1/tasks/{task_id}",
            json={"title": "Updated title"},
            timeout=10,
        )
        assert resp.status_code == 200
        body = resp.json()
        assert body["title"] == "Updated title"
        assert body["id"] == task_id

    def test_update_task_status_via_put(self):
        create_resp = requests.post(
            f"{BASE_URL}/api/v1/tasks",
            json={"title": "Status change via PUT"},
            timeout=10,
        )
        task_id = create_resp.json()["id"]
        resp = requests.put(
            f"{BASE_URL}/api/v1/tasks/{task_id}",
            json={"status": "in_progress"},
            timeout=10,
        )
        assert resp.status_code == 200
        assert resp.json()["status"] == "in_progress"

    def test_update_nonexistent_task(self):
        resp = requests.put(
            f"{BASE_URL}/api/v1/tasks/nonexistent-id-99999",
            json={"title": "No such task"},
            timeout=10,
        )
        assert resp.status_code == 404

    def test_update_task_invalid_status(self):
        create_resp = requests.post(
            f"{BASE_URL}/api/v1/tasks",
            json={"title": "For invalid status test"},
            timeout=10,
        )
        task_id = create_resp.json()["id"]
        resp = requests.put(
            f"{BASE_URL}/api/v1/tasks/{task_id}",
            json={"status": "invalid_status_value"},
            timeout=10,
        )
        assert resp.status_code == 400


class TestUpdateTaskStatus:
    """PATCH /api/v1/tasks/:id/status -- update task status."""

    def test_patch_status_success(self):
        create_resp = requests.post(
            f"{BASE_URL}/api/v1/tasks",
            json={"title": "For status patch"},
            timeout=10,
        )
        task_id = create_resp.json()["id"]
        resp = requests.patch(
            f"{BASE_URL}/api/v1/tasks/{task_id}/status",
            json={"status": "completed"},
            timeout=10,
        )
        assert resp.status_code == 200
        body = resp.json()
        assert body["status"] == "completed"
        assert body["completed_at"] is not None

    def test_patch_status_invalid(self):
        create_resp = requests.post(
            f"{BASE_URL}/api/v1/tasks",
            json={"title": "For invalid patch"},
            timeout=10,
        )
        task_id = create_resp.json()["id"]
        resp = requests.patch(
            f"{BASE_URL}/api/v1/tasks/{task_id}/status",
            json={"status": "bogus"},
            timeout=10,
        )
        assert resp.status_code == 400
        body = resp.json()
        assert body["error"]["code"] == "VALIDATION_ERROR"

    def test_patch_status_nonexistent_task(self):
        resp = requests.patch(
            f"{BASE_URL}/api/v1/tasks/nonexistent-id-00000/status",
            json={"status": "completed"},
            timeout=10,
        )
        assert resp.status_code == 404

    def test_patch_status_missing_body(self):
        create_resp = requests.post(
            f"{BASE_URL}/api/v1/tasks",
            json={"title": "For missing body patch"},
            timeout=10,
        )
        task_id = create_resp.json()["id"]
        resp = requests.patch(
            f"{BASE_URL}/api/v1/tasks/{task_id}/status",
            data="{}",
            headers={"Content-Type": "application/json"},
            timeout=10,
        )
        assert resp.status_code == 400


class TestDeleteTask:
    """DELETE /api/v1/tasks/:id -- delete a task."""

    def test_delete_task_success(self):
        create_resp = requests.post(
            f"{BASE_URL}/api/v1/tasks",
            json={"title": "To be deleted"},
            timeout=10,
        )
        task_id = create_resp.json()["id"]
        resp = requests.delete(
            f"{BASE_URL}/api/v1/tasks/{task_id}", timeout=10
        )
        assert resp.status_code == 204
        # Verify deletion
        get_resp = requests.get(
            f"{BASE_URL}/api/v1/tasks/{task_id}", timeout=10
        )
        assert get_resp.status_code == 404

    def test_delete_nonexistent_task(self):
        resp = requests.delete(
            f"{BASE_URL}/api/v1/tasks/nonexistent-id-delete", timeout=10
        )
        assert resp.status_code == 404


class TestCORS:
    """Verify CORS headers are present."""

    def test_cors_headers_present(self):
        resp = requests.get(f"{BASE_URL}/api/v1/tasks", timeout=10)
        assert resp.status_code == 200
        assert "Access-Control-Allow-Origin" in resp.headers
        assert resp.headers["Access-Control-Allow-Origin"] == "*"

    def test_options_preflight(self):
        resp = requests.options(f"{BASE_URL}/api/v1/tasks", timeout=10)
        assert resp.status_code == 204
        assert "Access-Control-Allow-Methods" in resp.headers
