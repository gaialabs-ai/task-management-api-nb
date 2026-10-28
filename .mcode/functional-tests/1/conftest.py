"""Shared fixtures for functional tests against the live Task API server.

The server is the real cmd/rest-server binary started by the lifecycle `run`
script (in-memory store, no auth). Every task a test creates is titled with an
`ftrun_<id>` prefix and deleted in teardown so reruns stay clean.

Each test records its primary request/response into a capture file
(FT_CAPTURE_FILE, default /tmp/ft_capture.json) so results can be reported
verbatim afterwards.
"""
import json
import os
import uuid

import pytest
import requests

BASE_URL = os.environ.get("TASK_API_BASE_URL", "http://localhost:%s" % os.environ.get("PORT", "8080"))
API = BASE_URL + "/api/v1/tasks"
CAPTURE_FILE = os.environ.get("FT_CAPTURE_FILE", "/tmp/ft_capture.json")
TIMEOUT = 10

_captures = {}
_current = {"name": None}


def record(resp, body=None):
    """Store the primary request/response for the currently running test."""
    req = resp.request
    _captures[_current["name"]] = {
        "method": req.method,
        "endpoint": req.path_url,
        "request_body": body if body is not None else (req.body.decode() if isinstance(req.body, bytes) else req.body),
        "status": resp.status_code,
        "response": resp.text,
    }
    return resp


@pytest.fixture(autouse=True)
def _track_test(request):
    _current["name"] = request.node.name
    resp = requests.get(BASE_URL + "/health", timeout=5)
    assert resp.status_code == 200, "server not healthy"
    yield


def pytest_sessionfinish(session, exitstatus):
    existing = {}
    if os.path.exists(CAPTURE_FILE):
        try:
            with open(CAPTURE_FILE) as f:
                existing = json.load(f)
        except Exception:
            existing = {}
    existing.update(_captures)
    with open(CAPTURE_FILE, "w") as f:
        json.dump(existing, f, indent=2)


class TaskFactory:
    def __init__(self):
        self.run_id = uuid.uuid4().hex[:8]
        self.created = []

    def create(self, label="task", **fields):
        payload = {"title": "ftrun_%s_%s" % (self.run_id, label)}
        payload.update(fields)
        resp = requests.post(API, json=payload, timeout=TIMEOUT)
        assert resp.status_code == 201, resp.text
        task = resp.json()
        self.created.append(task["id"])
        return task

    def set_status(self, task_id, status):
        resp = requests.patch("%s/%s/status" % (API, task_id), json={"status": status}, timeout=TIMEOUT)
        assert resp.status_code == 200, resp.text
        return resp.json()

    def cleanup(self):
        for task_id in self.created:
            requests.delete("%s/%s" % (API, task_id), timeout=TIMEOUT)


@pytest.fixture
def tasks():
    factory = TaskFactory()
    yield factory
    factory.cleanup()
