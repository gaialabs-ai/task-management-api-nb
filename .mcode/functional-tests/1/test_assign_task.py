"""POST /api/v1/tasks/:id/assign functional tests (live server)."""
import glob
import os
import time
import uuid
from datetime import datetime

import pytest
import requests

from conftest import API, TIMEOUT, record


def assign_url(task_id):
    return "%s/%s/assign" % (API, task_id)


def parse(ts):
    return datetime.fromisoformat(ts.replace("Z", "+00:00"))


def server_log_path():
    explicit = os.environ.get("FT_SERVER_LOG")
    if explicit:
        return explicit
    mcode = os.environ.get("MCODE_DIR")
    if not mcode:
        return None
    candidates = glob.glob(os.path.join(mcode, "lifecycle", "target", "runs", "*_run_stderr.log"))
    return max(candidates, key=os.path.getmtime) if candidates else None


class TestAssignHappyPath:
    def test_assign_task_success(self, tasks):
        t = tasks.create("assign")
        time.sleep(0.05)
        body = {"assigned_to": "alice@example.com"}
        resp = record(requests.post(assign_url(t["id"]), json=body, timeout=TIMEOUT))
        assert resp.status_code == 200, resp.text
        out = resp.json()
        assert out["id"] == t["id"]
        assert out["assigned_to"] == "alice@example.com"
        assert out["title"] == t["title"]
        assert parse(out["updated_at"]) > parse(t["updated_at"])
        assert out["created_at"] == t["created_at"]

    def test_assign_persists_on_get(self, tasks):
        t = tasks.create("persist")
        r = requests.post(assign_url(t["id"]), json={"assigned_to": "bob@example.com"}, timeout=TIMEOUT)
        assert r.status_code == 200
        resp = record(requests.get("%s/%s" % (API, t["id"]), timeout=TIMEOUT))
        assert resp.status_code == 200
        assert resp.json()["assigned_to"] == "bob@example.com"

    def test_assign_reassign_overwrites(self, tasks):
        t = tasks.create("reassign", assigned_to="first@example.com")
        resp = record(requests.post(assign_url(t["id"]), json={"assigned_to": "second@example.com"}, timeout=TIMEOUT))
        assert resp.status_code == 200
        assert resp.json()["assigned_to"] == "second@example.com"

    def test_assign_visible_in_list_filter(self, tasks):
        t = tasks.create("filter")
        email = "ftrun_%s@example.com" % uuid.uuid4().hex[:6]
        assert requests.post(assign_url(t["id"]), json={"assigned_to": email}, timeout=TIMEOUT).status_code == 200
        resp = record(requests.get(API, params={"assigned_to": email}, timeout=TIMEOUT))
        assert resp.status_code == 200
        assert [x["id"] for x in resp.json()["tasks"]] == [t["id"]]

    def test_assign_logs_notification(self, tasks):
        log_path = server_log_path()
        if not log_path or not os.path.exists(log_path):
            pytest.fail("server log not found (set FT_SERVER_LOG or MCODE_DIR)")
        t = tasks.create("notify")
        email = "notify_%s@example.com" % uuid.uuid4().hex[:6]
        resp = record(requests.post(assign_url(t["id"]), json={"assigned_to": email}, timeout=TIMEOUT))
        assert resp.status_code == 200
        expected = 'assignment notification: task %s "%s" assigned to %s' % (t["id"], t["title"], email)
        found = False
        for _ in range(20):
            with open(log_path) as f:
                if expected in f.read():
                    found = True
                    break
            time.sleep(0.1)
        assert found, "expected log line not found: %s" % expected


class TestAssignValidation:
    def _assert_validation(self, resp):
        assert resp.status_code == 400, resp.text
        assert resp.json()["error"]["code"] == "VALIDATION_ERROR"

    def test_assign_missing_assigned_to(self, tasks):
        t = tasks.create("missing")
        self._assert_validation(record(requests.post(assign_url(t["id"]), json={}, timeout=TIMEOUT)))
        # task must be unchanged
        assert "assigned_to" not in requests.get("%s/%s" % (API, t["id"]), timeout=TIMEOUT).json()

    def test_assign_empty_email(self, tasks):
        t = tasks.create("empty")
        self._assert_validation(record(requests.post(assign_url(t["id"]), json={"assigned_to": ""}, timeout=TIMEOUT)))

    def test_assign_invalid_email_format(self, tasks):
        t = tasks.create("invalid")
        self._assert_validation(record(requests.post(assign_url(t["id"]), json={"assigned_to": "not-an-email"}, timeout=TIMEOUT)))
        assert "assigned_to" not in requests.get("%s/%s" % (API, t["id"]), timeout=TIMEOUT).json()

    def test_assign_email_wrong_type(self, tasks):
        t = tasks.create("wrongtype")
        self._assert_validation(record(requests.post(assign_url(t["id"]), json={"assigned_to": 12345}, timeout=TIMEOUT)))

    def test_assign_malformed_json(self, tasks):
        t = tasks.create("malformed")
        raw = '{"assigned_to": "alice@example.com"'
        resp = requests.post(assign_url(t["id"]), data=raw, headers={"Content-Type": "application/json"}, timeout=TIMEOUT)
        self._assert_validation(record(resp, body=raw))


class TestAssignNotFound:
    def test_assign_unknown_task(self):
        missing = "00000000-0000-0000-0000-000000000000"
        resp = record(requests.post(assign_url(missing), json={"assigned_to": "alice@example.com"}, timeout=TIMEOUT))
        assert resp.status_code == 404
        err = resp.json()["error"]
        assert err["code"] == "NOT_FOUND"
        assert err["message"] == "task with ID %s not found" % missing
