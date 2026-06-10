"""Capture origin/target responses for all tests."""
import requests
import json
import time
import sys

BASE_URL = "http://localhost:8080"


def capture_all():
    results = {}

    # 1. Health check
    r = requests.get(f"{BASE_URL}/health", timeout=5)
    results["test_health_returns_200"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/health",
        "method": "GET",
    }

    # 2. List tasks (sample data)
    r = requests.get(f"{BASE_URL}/api/v1/tasks", timeout=10)
    results["test_list_tasks_returns_sample_data"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/api/v1/tasks",
        "method": "GET",
    }

    # 3. Pagination
    r = requests.get(f"{BASE_URL}/api/v1/tasks?page_size=1", timeout=10)
    results["test_list_tasks_pagination"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/api/v1/tasks?page_size=1",
        "method": "GET",
    }

    # 4. Filter by status
    r = requests.get(f"{BASE_URL}/api/v1/tasks?status=pending", timeout=10)
    results["test_list_tasks_filter_by_status"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/api/v1/tasks?status=pending",
        "method": "GET",
    }

    # 5. Filter by tags
    r = requests.get(f"{BASE_URL}/api/v1/tasks?tags=go", timeout=10)
    results["test_list_tasks_filter_by_tags"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/api/v1/tasks?tags=go",
        "method": "GET",
    }

    # 6. Sort by priority desc
    r = requests.get(f"{BASE_URL}/api/v1/tasks?sort_order=priority_desc", timeout=10)
    results["test_list_tasks_sort_by_priority_desc"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/api/v1/tasks?sort_order=priority_desc",
        "method": "GET",
    }

    # 7. Get existing task
    list_r = requests.get(f"{BASE_URL}/api/v1/tasks", timeout=10)
    task_id = list_r.json()["tasks"][0]["id"]
    r = requests.get(f"{BASE_URL}/api/v1/tasks/{task_id}", timeout=10)
    results["test_get_existing_task"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": f"/api/v1/tasks/{task_id}",
        "method": "GET",
    }

    # 8. Get nonexistent task
    r = requests.get(f"{BASE_URL}/api/v1/tasks/nonexistent-id-12345", timeout=10)
    results["test_get_nonexistent_task"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/api/v1/tasks/nonexistent-id-12345",
        "method": "GET",
    }

    # 9. Create task success
    payload = {"title": "Test task creation", "description": "A test task", "priority": "high", "tags": ["test", "functional"]}
    r = requests.post(f"{BASE_URL}/api/v1/tasks", json=payload, timeout=10)
    results["test_create_task_success"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_body": json.dumps(payload),
    }

    # 10. Create task missing title
    payload = {"description": "No title provided"}
    r = requests.post(f"{BASE_URL}/api/v1/tasks", json=payload, timeout=10)
    results["test_create_task_missing_title"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_body": json.dumps(payload),
    }

    # 11. Create task empty body
    r = requests.post(f"{BASE_URL}/api/v1/tasks", data="{}", headers={"Content-Type": "application/json"}, timeout=10)
    results["test_create_task_empty_body"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_body": "{}",
    }

    # 12. Create task default priority
    payload = {"title": "Task with default priority"}
    r = requests.post(f"{BASE_URL}/api/v1/tasks", json=payload, timeout=10)
    results["test_create_task_default_priority"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_body": json.dumps(payload),
    }

    # 13. Create task invalid priority
    payload = {"title": "Task with bad priority", "priority": "urgent"}
    r = requests.post(f"{BASE_URL}/api/v1/tasks", json=payload, timeout=10)
    results["test_create_task_invalid_priority"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_body": json.dumps(payload),
    }

    # 14. Create task with assigned_to
    payload = {"title": "Assigned task", "assigned_to": "user@example.com"}
    r = requests.post(f"{BASE_URL}/api/v1/tasks", json=payload, timeout=10)
    results["test_create_task_with_assigned_to"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_body": json.dumps(payload),
    }

    # 15. Update task title
    cr = requests.post(f"{BASE_URL}/api/v1/tasks", json={"title": "Original title"}, timeout=10)
    tid = cr.json()["id"]
    payload = {"title": "Updated title"}
    r = requests.put(f"{BASE_URL}/api/v1/tasks/{tid}", json=payload, timeout=10)
    results["test_update_task_title"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": f"/api/v1/tasks/:id",
        "method": "PUT",
        "request_body": json.dumps(payload),
    }

    # 16. Update task status via PUT
    cr = requests.post(f"{BASE_URL}/api/v1/tasks", json={"title": "Status change via PUT"}, timeout=10)
    tid = cr.json()["id"]
    payload = {"status": "in_progress"}
    r = requests.put(f"{BASE_URL}/api/v1/tasks/{tid}", json=payload, timeout=10)
    results["test_update_task_status_via_put"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": f"/api/v1/tasks/:id",
        "method": "PUT",
        "request_body": json.dumps(payload),
    }

    # 17. Update nonexistent task
    payload = {"title": "No such task"}
    r = requests.put(f"{BASE_URL}/api/v1/tasks/nonexistent-id-99999", json=payload, timeout=10)
    results["test_update_nonexistent_task"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/api/v1/tasks/nonexistent-id-99999",
        "method": "PUT",
        "request_body": json.dumps(payload),
    }

    # 18. Update task invalid status
    cr = requests.post(f"{BASE_URL}/api/v1/tasks", json={"title": "For invalid status test"}, timeout=10)
    tid = cr.json()["id"]
    payload = {"status": "invalid_status_value"}
    r = requests.put(f"{BASE_URL}/api/v1/tasks/{tid}", json=payload, timeout=10)
    results["test_update_task_invalid_status"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": f"/api/v1/tasks/:id",
        "method": "PUT",
        "request_body": json.dumps(payload),
    }

    # 19. Patch status success
    cr = requests.post(f"{BASE_URL}/api/v1/tasks", json={"title": "For status patch"}, timeout=10)
    tid = cr.json()["id"]
    payload = {"status": "completed"}
    r = requests.patch(f"{BASE_URL}/api/v1/tasks/{tid}/status", json=payload, timeout=10)
    results["test_patch_status_success"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": f"/api/v1/tasks/:id/status",
        "method": "PATCH",
        "request_body": json.dumps(payload),
    }

    # 20. Patch status invalid
    cr = requests.post(f"{BASE_URL}/api/v1/tasks", json={"title": "For invalid patch"}, timeout=10)
    tid = cr.json()["id"]
    payload = {"status": "bogus"}
    r = requests.patch(f"{BASE_URL}/api/v1/tasks/{tid}/status", json=payload, timeout=10)
    results["test_patch_status_invalid"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": f"/api/v1/tasks/:id/status",
        "method": "PATCH",
        "request_body": json.dumps(payload),
    }

    # 21. Patch status nonexistent task
    payload = {"status": "completed"}
    r = requests.patch(f"{BASE_URL}/api/v1/tasks/nonexistent-id-00000/status", json=payload, timeout=10)
    results["test_patch_status_nonexistent_task"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/api/v1/tasks/nonexistent-id-00000/status",
        "method": "PATCH",
        "request_body": json.dumps(payload),
    }

    # 22. Patch status missing body
    cr = requests.post(f"{BASE_URL}/api/v1/tasks", json={"title": "For missing body patch"}, timeout=10)
    tid = cr.json()["id"]
    r = requests.patch(f"{BASE_URL}/api/v1/tasks/{tid}/status", data="{}", headers={"Content-Type": "application/json"}, timeout=10)
    results["test_patch_status_missing_body"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": f"/api/v1/tasks/:id/status",
        "method": "PATCH",
        "request_body": "{}",
    }

    # 23. Delete task success
    cr = requests.post(f"{BASE_URL}/api/v1/tasks", json={"title": "To be deleted"}, timeout=10)
    tid = cr.json()["id"]
    r = requests.delete(f"{BASE_URL}/api/v1/tasks/{tid}", timeout=10)
    results["test_delete_task_success"] = {
        "status": r.status_code,
        "body": r.text if r.text else "",
        "endpoint": f"/api/v1/tasks/:id",
        "method": "DELETE",
    }

    # 24. Delete nonexistent task
    r = requests.delete(f"{BASE_URL}/api/v1/tasks/nonexistent-id-delete", timeout=10)
    results["test_delete_nonexistent_task"] = {
        "status": r.status_code,
        "body": r.text,
        "endpoint": "/api/v1/tasks/nonexistent-id-delete",
        "method": "DELETE",
    }

    # 25. CORS headers
    r = requests.get(f"{BASE_URL}/api/v1/tasks", timeout=10)
    cors_value = r.headers.get("Access-Control-Allow-Origin", "")
    results["test_cors_headers_present"] = {
        "status": r.status_code,
        "body": json.dumps({"Access-Control-Allow-Origin": cors_value}),
        "endpoint": "/api/v1/tasks",
        "method": "GET",
    }

    # 26. OPTIONS preflight
    r = requests.options(f"{BASE_URL}/api/v1/tasks", timeout=10)
    methods_value = r.headers.get("Access-Control-Allow-Methods", "")
    results["test_options_preflight"] = {
        "status": r.status_code,
        "body": json.dumps({"Access-Control-Allow-Methods": methods_value}),
        "endpoint": "/api/v1/tasks",
        "method": "OPTIONS",
    }

    return results


if __name__ == "__main__":
    results = capture_all()
    json.dump(results, sys.stdout, indent=2)
