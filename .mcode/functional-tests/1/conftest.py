import json
import os
import sys

import pytest
import requests

sys.path.insert(0, os.path.dirname(__file__))
from helpers import BASE_URL  # noqa: E402

# Optional evidence capture: set FT_CAPTURE=/path/file.json to dump, per test, the last HTTP exchange.
_CAPTURE = os.environ.get("FT_CAPTURE")
_state = {"current": None, "records": {}}

if _CAPTURE:
    _orig_request = requests.Session.request

    def _wrapped(self, method, url, **kwargs):
        resp = _orig_request(self, method, url, **kwargs)
        cur = _state["current"]
        if cur is not None:
            body = kwargs.get("json")
            if body is None and kwargs.get("data") is not None:
                body = kwargs.get("data")
            _state["records"].setdefault(cur, []).append({
                "method": method.upper(), "url": resp.request.url, "request_body": body,
                "status": resp.status_code, "response": resp.text[:4000],
            })
        return resp

    requests.Session.request = _wrapped


@pytest.fixture(autouse=True)
def _capture_current(request):
    _state["current"] = request.node.nodeid
    yield
    _state["current"] = None


def pytest_sessionfinish(session, exitstatus):
    if _CAPTURE:
        with open(_CAPTURE, "w") as fh:
            json.dump(_state["records"], fh, indent=1)


@pytest.fixture(autouse=True, scope="session")
def server_is_healthy():
    resp = requests.get(BASE_URL + "/health", timeout=5)
    assert resp.status_code == 200
    assert resp.json()["status"] == "healthy"
