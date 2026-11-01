import json, os
import pytest, requests

BASE = os.environ.get("BASE_URL", "http://localhost:8080")
LOG = os.path.join(os.path.dirname(__file__), "last_responses.json")
_state = {}

@pytest.fixture(autouse=True)
def health():
    assert requests.get(f"{BASE}/health", timeout=5).status_code == 200

@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_call(item):
    _state["cur"] = item.name
    yield

def call(method, path, **kw):
    r = requests.request(method, BASE + path, timeout=10, **kw)
    _state[_state["cur"]] = {"method": method, "path": path, "body": kw.get("json"),
                            "status": r.status_code, "out": r.text}
    with open(LOG, "w") as f:
        json.dump({k: v for k, v in _state.items() if k != "cur"}, f)
    return r
