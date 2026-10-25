"""Shared helpers for task-management-api functional tests (real HTTP, no mocks)."""
import os
import uuid
from datetime import datetime, timedelta, timezone

import requests

BASE_URL = os.environ.get("FT_BASE_URL", "http://localhost:" + os.environ.get("PORT", "8080"))
API = BASE_URL + "/api/v1"
TIMEOUT = 10


def new_run_id():
    return "ftrun_" + uuid.uuid4().hex[:10]


def iso(dt):
    return dt.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def days_ago(n):
    return iso(datetime.now(timezone.utc) - timedelta(days=n))


def days_ahead(n):
    return iso(datetime.now(timezone.utc) + timedelta(days=n))


def create_task(title, **extra):
    body = {"title": title}
    body.update(extra)
    resp = requests.post(API + "/tasks", json=body, timeout=TIMEOUT)
    assert resp.status_code == 201, "create failed: %s %s" % (resp.status_code, resp.text)
    return resp.json()


def patch_status(task_id, status):
    return requests.patch(API + "/tasks/%s/status" % task_id, json={"status": status}, timeout=TIMEOUT)


def assign_task(task_id, payload):
    return requests.post(API + "/tasks/%s/assign" % task_id, json=payload, timeout=TIMEOUT)


def parse_ts(ts):
    # Go RFC3339Nano: trim fractional seconds to microseconds for fromisoformat
    ts = ts.replace("Z", "+00:00")
    if "." in ts:
        head, rest = ts.split(".", 1)
        frac = ""
        i = 0
        while i < len(rest) and rest[i].isdigit():
            frac += rest[i]
            i += 1
        ts = head + "." + frac[:6].ljust(6, "0") + rest[i:]
    return datetime.fromisoformat(ts)


def set_status(task_id, status):
    resp = patch_status(task_id, status)
    assert resp.status_code == 200, "status update failed: %s %s" % (resp.status_code, resp.text)
    return resp.json()


def get_task(task_id):
    return requests.get(API + "/tasks/%s" % task_id, timeout=TIMEOUT)


def walk_pages(path, page_size, params=None, max_pages=500):
    """Follow next_page_token until exhausted. Returns (all_tasks, pages, last_body)."""
    tasks, pages = [], []
    token = None
    for _ in range(max_pages):
        q = dict(params or {})
        q["page_size"] = page_size
        if token:
            q["page_token"] = token
        resp = requests.get(API + path, params=q, timeout=TIMEOUT)
        assert resp.status_code == 200, resp.text
        body = resp.json()
        pages.append(body)
        tasks.extend(body["tasks"] or [])
        token = body.get("next_page_token")
        if not token:
            return tasks, pages, body
    raise AssertionError("pagination did not terminate")


def assert_error(resp, status, code):
    assert resp.status_code == status, "expected %s got %s: %s" % (status, resp.status_code, resp.text)
    body = resp.json()
    assert "error" in body, body
    assert body["error"]["code"] == code, body
    assert isinstance(body["error"]["message"], str) and body["error"]["message"], body
