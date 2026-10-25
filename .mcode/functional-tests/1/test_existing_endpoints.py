"""Regression for pre-existing endpoints: health, CRUD, status, search, stats, comments."""
import requests

from helpers import API, BASE_URL, TIMEOUT, assert_error, create_task, new_run_id, set_status


class TestHealth:
    def test_health(self):
        resp = requests.get(BASE_URL + "/health", timeout=TIMEOUT)
        assert resp.status_code == 200
        body = resp.json()
        assert body["status"] == "healthy" and body["service"] == "task-api-go"


class TestTaskCrud:
    def test_create_get_update_delete_cycle(self):
        run = new_run_id()
        resp = requests.post(API + "/tasks", json={"title": run + " crud", "priority": "high", "tags": ["x"]},
                             timeout=TIMEOUT)
        assert resp.status_code == 201
        task = resp.json()
        assert resp.headers["Location"] == "/api/v1/tasks/" + task["id"]
        assert task["status"] == "pending" and task["priority"] == "high"

        assert requests.get(API + "/tasks/" + task["id"], timeout=TIMEOUT).json()["title"] == run + " crud"

        upd = requests.put(API + "/tasks/" + task["id"], json={"title": run + " renamed", "description": "d"},
                           timeout=TIMEOUT)
        assert upd.status_code == 200 and upd.json()["title"] == run + " renamed"

        assert requests.delete(API + "/tasks/" + task["id"], timeout=TIMEOUT).status_code == 204
        assert_error(requests.get(API + "/tasks/" + task["id"], timeout=TIMEOUT), 404, "NOT_FOUND")
        assert_error(requests.delete(API + "/tasks/" + task["id"], timeout=TIMEOUT), 404, "NOT_FOUND")

    def test_create_validation(self):
        assert_error(requests.post(API + "/tasks", json={}, timeout=TIMEOUT), 400, "VALIDATION_ERROR")
        assert_error(requests.post(API + "/tasks", json={"title": "t", "priority": "bogus"}, timeout=TIMEOUT),
                     400, "VALIDATION_ERROR")

    def test_default_priority_and_empty_tags(self):
        t = create_task(new_run_id() + " defaults")
        assert t["priority"] == "medium" and t["tags"] == []

    def test_patch_status_sets_completed_at(self):
        t = create_task(new_run_id() + " completeme")
        done = set_status(t["id"], "completed")
        assert done["status"] == "completed" and done.get("completed_at")

    def test_patch_status_invalid_and_not_found(self):
        t = create_task(new_run_id() + " badstatus")
        r = requests.patch(API + "/tasks/%s/status" % t["id"], json={"status": "nope"}, timeout=TIMEOUT)
        assert_error(r, 400, "VALIDATION_ERROR")
        r = requests.patch(API + "/tasks/nonexistent/status", json={"status": "pending"}, timeout=TIMEOUT)
        assert_error(r, 404, "NOT_FOUND")

    def test_update_not_found(self):
        r = requests.put(API + "/tasks/nonexistent", json={"title": "x"}, timeout=TIMEOUT)
        assert_error(r, 404, "NOT_FOUND")


class TestSearchAndStats:
    def test_search_finds_title_match(self):
        run = new_run_id()
        t = create_task(run + " findable")
        body = requests.get(API + "/tasks/search", params={"q": run}, timeout=TIMEOUT).json()
        assert body["total_count"] == 1 and body["tasks"][0]["id"] == t["id"] and body["query"] == run

    def test_search_requires_q(self):
        assert_error(requests.get(API + "/tasks/search", timeout=TIMEOUT), 400, "VALIDATION_ERROR")

    def test_stats_shape_and_consistency(self):
        body = requests.get(API + "/tasks/stats", timeout=TIMEOUT).json()
        assert set(body["by_status"]) >= {"pending", "in_progress", "completed", "cancelled", "on_hold"}
        assert set(body["by_priority"]) >= {"low", "medium", "high", "critical"}
        assert sum(body["by_status"].values()) == body["total"]
        assert sum(body["by_priority"].values()) == body["total"]


class TestComments:
    def test_comment_lifecycle(self):
        t = create_task(new_run_id() + " commented")
        base = API + "/tasks/%s/comments" % t["id"]
        resp = requests.post(base, json={"body": "hello", "author": "a@example.com"}, timeout=TIMEOUT)
        assert resp.status_code == 201, resp.text
        comment = resp.json()
        listing = requests.get(base, timeout=TIMEOUT)
        assert listing.status_code == 200
        assert comment["id"] in [c["id"] for c in listing.json()["comments"]]
        assert requests.delete(base + "/" + comment["id"], timeout=TIMEOUT).status_code == 204
        assert comment["id"] not in [c["id"] for c in requests.get(base, timeout=TIMEOUT).json()["comments"]]

    def test_comments_on_missing_task(self):
        assert_error(requests.get(API + "/tasks/nonexistent/comments", timeout=TIMEOUT), 404, "NOT_FOUND")
