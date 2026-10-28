"""Regression tests: existing endpoints keep their behavior (live server)."""
import requests

from conftest import API, TIMEOUT, record


class TestHealthAndList:
    def test_list_tasks_includes_sample_data(self):
        resp = record(requests.get(API, timeout=TIMEOUT))
        assert resp.status_code == 200
        body = resp.json()
        titles = [t["title"] for t in body["tasks"]]
        assert "Implement Go REST API" in titles
        assert "Add gRPC support" in titles
        assert body["total_count"] >= 2

    def test_list_tasks_pagination_unchanged(self, tasks):
        for i in range(3):
            tasks.create("lp%d" % i)
        total = requests.get(API, timeout=TIMEOUT).json()["total_count"]
        resp = record(requests.get(API, params={"page_size": 2}, timeout=TIMEOUT))
        assert resp.status_code == 200
        body = resp.json()
        assert len(body["tasks"]) == 2
        assert body["next_page_token"] == "2"
        assert body["total_count"] == total

    def test_list_tasks_page_size_101_rejected(self):
        resp = record(requests.get(API, params={"page_size": 101}, timeout=TIMEOUT))
        assert resp.status_code == 400
        assert resp.json()["error"]["code"] == "VALIDATION_ERROR"


class TestTaskCrud:
    def test_create_task_success(self, tasks):
        payload = {"title": "ftrun_crud_create", "priority": "high", "tags": ["a", "b"]}
        resp = record(requests.post(API, json=payload, timeout=TIMEOUT))
        assert resp.status_code == 201
        body = resp.json()
        tasks.created.append(body["id"])
        assert body["title"] == "ftrun_crud_create"
        assert body["status"] == "pending"
        assert body["priority"] == "high"
        assert resp.headers.get("Location") == "/api/v1/tasks/" + body["id"]

    def test_create_task_missing_title(self):
        resp = record(requests.post(API, json={"description": "no title"}, timeout=TIMEOUT))
        assert resp.status_code == 400
        assert resp.json()["error"]["code"] == "VALIDATION_ERROR"

    def test_create_task_invalid_priority(self):
        resp = record(requests.post(API, json={"title": "ftrun_badprio", "priority": "urgent"}, timeout=TIMEOUT))
        assert resp.status_code == 400
        assert resp.json()["error"]["code"] == "VALIDATION_ERROR"

    def test_get_task_success(self, tasks):
        t = tasks.create("get")
        resp = record(requests.get("%s/%s" % (API, t["id"]), timeout=TIMEOUT))
        assert resp.status_code == 200
        assert resp.json()["id"] == t["id"]

    def test_get_task_not_found(self):
        resp = record(requests.get(API + "/does-not-exist", timeout=TIMEOUT))
        assert resp.status_code == 404
        assert resp.json()["error"]["code"] == "NOT_FOUND"

    def test_update_task_success(self, tasks):
        t = tasks.create("upd")
        body = {"title": "ftrun_updated", "priority": "low"}
        resp = record(requests.put("%s/%s" % (API, t["id"]), json=body, timeout=TIMEOUT))
        assert resp.status_code == 200
        assert resp.json()["title"] == "ftrun_updated"
        assert resp.json()["priority"] == "low"

    def test_update_task_not_found(self):
        resp = record(requests.put(API + "/does-not-exist", json={"title": "x"}, timeout=TIMEOUT))
        assert resp.status_code == 404
        assert resp.json()["error"]["code"] == "NOT_FOUND"

    def test_patch_status_completed(self, tasks):
        t = tasks.create("status")
        resp = record(requests.patch("%s/%s/status" % (API, t["id"]), json={"status": "completed"}, timeout=TIMEOUT))
        assert resp.status_code == 200
        body = resp.json()
        assert body["status"] == "completed"
        assert body.get("completed_at")

    def test_patch_status_invalid(self, tasks):
        t = tasks.create("badstatus")
        resp = record(requests.patch("%s/%s/status" % (API, t["id"]), json={"status": "bogus"}, timeout=TIMEOUT))
        assert resp.status_code == 400
        assert resp.json()["error"]["code"] == "VALIDATION_ERROR"

    def test_delete_task_then_404(self, tasks):
        t = tasks.create("del")
        resp = record(requests.delete("%s/%s" % (API, t["id"]), timeout=TIMEOUT))
        assert resp.status_code == 204
        assert requests.get("%s/%s" % (API, t["id"]), timeout=TIMEOUT).status_code == 404


class TestSearchAndStats:
    def test_search_tasks(self, tasks):
        t = tasks.create("zebrasearchterm")
        resp = record(requests.get(API + "/search", params={"q": "zebrasearchterm"}, timeout=TIMEOUT))
        assert resp.status_code == 200
        body = resp.json()
        assert body["query"] == "zebrasearchterm"
        assert body["total_count"] == 1
        assert body["tasks"][0]["id"] == t["id"]

    def test_search_missing_query(self):
        resp = record(requests.get(API + "/search", timeout=TIMEOUT))
        assert resp.status_code == 400
        assert resp.json()["error"]["code"] == "VALIDATION_ERROR"

    def test_stats_counts(self, tasks):
        before = requests.get(API + "/stats", timeout=TIMEOUT).json()
        tasks.create("stat", priority="critical")
        resp = record(requests.get(API + "/stats", timeout=TIMEOUT))
        assert resp.status_code == 200
        after = resp.json()
        assert after["total"] == before["total"] + 1
        assert after["by_priority"]["critical"] == before["by_priority"]["critical"] + 1
        assert set(after["by_status"]) == {"pending", "in_progress", "completed", "cancelled", "on_hold"}


class TestComments:
    def test_comments_add_list_delete(self, tasks):
        t = tasks.create("comments")
        url = "%s/%s/comments" % (API, t["id"])
        add = requests.post(url, json={"author": "ft", "body": "hello"}, timeout=TIMEOUT)
        assert add.status_code == 201, add.text
        cid = add.json()["id"]
        resp = record(requests.get(url, timeout=TIMEOUT))
        assert resp.status_code == 200
        assert resp.json()["total"] == 1
        assert requests.delete("%s/%s" % (url, cid), timeout=TIMEOUT).status_code == 204
        assert requests.get(url, timeout=TIMEOUT).json()["total"] == 0

    def test_comments_unknown_task(self):
        resp = record(requests.get(API + "/does-not-exist/comments", timeout=TIMEOUT))
        assert resp.status_code == 404
        assert resp.json()["error"]["code"] == "NOT_FOUND"
