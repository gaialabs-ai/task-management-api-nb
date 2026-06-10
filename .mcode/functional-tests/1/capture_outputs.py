"""
Capture actual response outputs from the running API for recording test results.
This script runs each test scenario and captures the response bodies and status codes.
"""

import requests
import json
import time

BASE_URL = "http://localhost:8080"


def capture_all():
    """Run all test scenarios and capture outputs."""
    results = {}

    # 1. Health check
    resp = requests.get(f"{BASE_URL}/health", timeout=5)
    results["test_health_check_returns_200"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": "/health",
        "method": "GET",
    }

    # 2. Create task success
    payload = {
        "title": "Test Task Creation",
        "description": "A task created by functional test",
        "priority": "high",
        "tags": ["test", "functional"],
    }
    resp = requests.post(f"{BASE_URL}/api/v1/tasks", json=payload, timeout=10)
    results["test_create_task_success"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_body": json.dumps(payload),
        "has_location": "Location" in resp.headers,
    }
    created_task_id = resp.json().get("id", "") if resp.status_code == 201 else ""

    # 3. Create task missing title
    payload_no_title = {"description": "No title provided"}
    resp = requests.post(f"{BASE_URL}/api/v1/tasks", json=payload_no_title, timeout=10)
    results["test_create_task_missing_title"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_body": json.dumps(payload_no_title),
    }

    # 4. Create task empty title
    payload_empty = {"title": ""}
    resp = requests.post(f"{BASE_URL}/api/v1/tasks", json=payload_empty, timeout=10)
    results["test_create_task_empty_title"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_body": json.dumps(payload_empty),
    }

    # 5. Create task default priority
    payload_default = {"title": "Task with default priority"}
    resp = requests.post(f"{BASE_URL}/api/v1/tasks", json=payload_default, timeout=10)
    results["test_create_task_default_priority"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_body": json.dumps(payload_default),
    }

    # 6. Create task with assigned_to
    payload_assigned = {"title": "Assigned Task", "assigned_to": "alice"}
    resp = requests.post(f"{BASE_URL}/api/v1/tasks", json=payload_assigned, timeout=10)
    results["test_create_task_with_assigned_to"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_body": json.dumps(payload_assigned),
    }

    # 7. Get task success (create one first)
    create_resp = requests.post(f"{BASE_URL}/api/v1/tasks", json={"title": "Task to Get", "priority": "low"}, timeout=10)
    get_task_id = create_resp.json()["id"]
    resp = requests.get(f"{BASE_URL}/api/v1/tasks/{get_task_id}", timeout=10)
    results["test_get_task_success"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": f"/api/v1/tasks/{get_task_id}",
        "method": "GET",
    }

    # 8. Get task not found
    resp = requests.get(f"{BASE_URL}/api/v1/tasks/nonexistent-uuid-12345", timeout=10)
    results["test_get_task_not_found"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": "/api/v1/tasks/nonexistent-uuid-12345",
        "method": "GET",
    }

    # 9. List tasks
    resp = requests.get(f"{BASE_URL}/api/v1/tasks", timeout=10)
    results["test_list_tasks_returns_tasks"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": "/api/v1/tasks",
        "method": "GET",
    }

    # 10. List tasks with page_size
    # Create extra tasks
    for i in range(3):
        requests.post(f"{BASE_URL}/api/v1/tasks", json={"title": f"Paginate Task {i}"}, timeout=10)
    resp = requests.get(f"{BASE_URL}/api/v1/tasks", params={"page_size": 1}, timeout=10)
    results["test_list_tasks_with_page_size"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": "/api/v1/tasks",
        "method": "GET",
    }

    # 11. List tasks pagination
    resp1 = requests.get(f"{BASE_URL}/api/v1/tasks", params={"page_size": 2}, timeout=10)
    body1 = resp1.json()
    page2_body = ""
    if body1.get("next_page_token"):
        resp2 = requests.get(f"{BASE_URL}/api/v1/tasks", params={"page_size": 2, "page_token": body1["next_page_token"]}, timeout=10)
        page2_body = resp2.text
    results["test_list_tasks_pagination"] = {
        "status": resp1.status_code,
        "body": resp1.text,
        "page2_body": page2_body,
        "endpoint": "/api/v1/tasks",
        "method": "GET",
    }

    # 12. Filter by status
    resp = requests.get(f"{BASE_URL}/api/v1/tasks", params={"status": "pending"}, timeout=10)
    results["test_list_tasks_filter_by_status"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": "/api/v1/tasks",
        "method": "GET",
    }

    # 13. Filter nonexistent status (completed - no tasks should be completed yet after fresh start)
    resp = requests.get(f"{BASE_URL}/api/v1/tasks", params={"status": "completed"}, timeout=10)
    results["test_list_tasks_filter_nonexistent_status"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": "/api/v1/tasks",
        "method": "GET",
    }

    # 14. Update task title
    create_resp = requests.post(f"{BASE_URL}/api/v1/tasks", json={"title": "Original Title"}, timeout=10)
    update_id = create_resp.json()["id"]
    resp = requests.put(f"{BASE_URL}/api/v1/tasks/{update_id}", json={"title": "Updated Title"}, timeout=10)
    results["test_update_task_title"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": f"/api/v1/tasks/{update_id}",
        "method": "PUT",
        "request_body": json.dumps({"title": "Updated Title"}),
    }

    # 15. Update multiple fields
    create_resp = requests.post(f"{BASE_URL}/api/v1/tasks", json={"title": "Multi Update Task"}, timeout=10)
    multi_id = create_resp.json()["id"]
    multi_payload = {"title": "Multi Updated", "description": "New description", "priority": "critical", "tags": ["updated", "multi"]}
    resp = requests.put(f"{BASE_URL}/api/v1/tasks/{multi_id}", json=multi_payload, timeout=10)
    results["test_update_task_multiple_fields"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": f"/api/v1/tasks/{multi_id}",
        "method": "PUT",
        "request_body": json.dumps(multi_payload),
    }

    # 16. Update task not found
    resp = requests.put(f"{BASE_URL}/api/v1/tasks/nonexistent-uuid-99999", json={"title": "Not Found"}, timeout=10)
    results["test_update_task_not_found"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": "/api/v1/tasks/nonexistent-uuid-99999",
        "method": "PUT",
        "request_body": json.dumps({"title": "Not Found"}),
    }

    # 17. Update status success
    create_resp = requests.post(f"{BASE_URL}/api/v1/tasks", json={"title": "Status Update Task"}, timeout=10)
    status_id = create_resp.json()["id"]
    resp = requests.patch(f"{BASE_URL}/api/v1/tasks/{status_id}/status", json={"status": "in_progress"}, timeout=10)
    results["test_update_status_success"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": f"/api/v1/tasks/{status_id}/status",
        "method": "PATCH",
        "request_body": json.dumps({"status": "in_progress"}),
    }

    # 18. Update status to completed
    create_resp = requests.post(f"{BASE_URL}/api/v1/tasks", json={"title": "Complete Me"}, timeout=10)
    complete_id = create_resp.json()["id"]
    resp = requests.patch(f"{BASE_URL}/api/v1/tasks/{complete_id}/status", json={"status": "completed"}, timeout=10)
    results["test_update_status_to_completed_sets_completed_at"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": f"/api/v1/tasks/{complete_id}/status",
        "method": "PATCH",
        "request_body": json.dumps({"status": "completed"}),
    }

    # 19. Update status invalid
    create_resp = requests.post(f"{BASE_URL}/api/v1/tasks", json={"title": "Invalid Status Task"}, timeout=10)
    invalid_status_id = create_resp.json()["id"]
    resp = requests.patch(f"{BASE_URL}/api/v1/tasks/{invalid_status_id}/status", json={"status": "invalid_status"}, timeout=10)
    results["test_update_status_invalid"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": f"/api/v1/tasks/{invalid_status_id}/status",
        "method": "PATCH",
        "request_body": json.dumps({"status": "invalid_status"}),
    }

    # 20. Update status not found
    resp = requests.patch(f"{BASE_URL}/api/v1/tasks/nonexistent-uuid-88888/status", json={"status": "in_progress"}, timeout=10)
    results["test_update_status_not_found"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": "/api/v1/tasks/nonexistent-uuid-88888/status",
        "method": "PATCH",
        "request_body": json.dumps({"status": "in_progress"}),
    }

    # 21. Delete task success
    create_resp = requests.post(f"{BASE_URL}/api/v1/tasks", json={"title": "Task to Delete"}, timeout=10)
    delete_id = create_resp.json()["id"]
    resp = requests.delete(f"{BASE_URL}/api/v1/tasks/{delete_id}", timeout=10)
    results["test_delete_task_success"] = {
        "status": resp.status_code,
        "body": resp.text if resp.text else "",
        "endpoint": f"/api/v1/tasks/{delete_id}",
        "method": "DELETE",
    }

    # 22. Delete task not found
    resp = requests.delete(f"{BASE_URL}/api/v1/tasks/nonexistent-uuid-77777", timeout=10)
    results["test_delete_task_not_found"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": "/api/v1/tasks/nonexistent-uuid-77777",
        "method": "DELETE",
    }

    # 23. CORS headers present
    resp = requests.get(f"{BASE_URL}/health", headers={"Origin": "http://localhost:3000"}, timeout=5)
    results["test_cors_headers_present"] = {
        "status": resp.status_code,
        "body": resp.text,
        "endpoint": "/health",
        "method": "GET",
        "cors_header": resp.headers.get("Access-Control-Allow-Origin", ""),
    }

    # 24. CORS options preflight
    resp = requests.options(
        f"{BASE_URL}/api/v1/tasks",
        headers={
            "Origin": "http://localhost:3000",
            "Access-Control-Request-Method": "POST",
            "Access-Control-Request-Headers": "Content-Type",
        },
        timeout=5,
    )
    results["test_cors_options_preflight"] = {
        "status": resp.status_code,
        "body": resp.text if resp.text else "",
        "endpoint": "/api/v1/tasks",
        "method": "OPTIONS",
        "cors_header": resp.headers.get("Access-Control-Allow-Origin", ""),
    }

    return results


if __name__ == "__main__":
    results = capture_all()
    print(json.dumps(results, indent=2))
