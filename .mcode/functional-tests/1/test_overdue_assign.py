import uuid
from datetime import datetime, timedelta, timezone
import pytest
import requests

BASE = "http://localhost:8080"
API = BASE + "/api/v1/tasks"


def iso(delta_days):
    return (datetime.now(timezone.utc) + timedelta(days=delta_days)).strftime("%Y-%m-%dT%H:%M:%SZ")


def create(tag, title, days=None, **kw):
    body = {"title": title, "tags": [tag]}
    if days is not None:
        body["due_date"] = iso(days)
    body.update(kw)
    r = requests.post(API, json=body, timeout=10)
    assert r.status_code == 201, r.text
    return r.json()


@pytest.fixture(autouse=True)
def health():
    assert requests.get(BASE + "/health", timeout=5).status_code == 200


@pytest.fixture
def seeded():
    tag = "ftrun_" + uuid.uuid4().hex[:8]
    ids = {
        "old3": create(tag, "old3", -3)["id"],
        "old1": create(tag, "old1", -1)["id"],
        "old2": create(tag, "old2", -2)["id"],
        "future": create(tag, "future", 5)["id"],
        "nodue": create(tag, "nodue")["id"],
    }
    yield tag, ids
    for i in ids.values():
        requests.delete(f"{API}/{i}", timeout=10)


def overdue(tag, **params):
    p = {"tags": tag}
    p.update(params)
    return requests.get(API + "/overdue", params=p, timeout=10)


def test_overdue_lists_only_past_due_sorted(seeded):
    tag, ids = seeded
    r = overdue(tag)
    assert r.status_code == 200
    j = r.json()
    got = [t["id"] for t in j["tasks"]]
    assert got == [ids["old3"], ids["old2"], ids["old1"]]
    assert j["total_count"] == 3


def test_overdue_excludes_completed_and_cancelled(seeded):
    tag, ids = seeded
    r = requests.put(f"{API}/{ids['old1']}", json={"status": "completed"}, timeout=10)
    assert r.status_code == 200, r.text
    r = requests.put(f"{API}/{ids['old2']}", json={"status": "cancelled"}, timeout=10)
    assert r.status_code == 200, r.text
    j = overdue(tag).json()
    assert [t["id"] for t in j["tasks"]] == [ids["old3"]]
    assert j["total_count"] == 1


def test_overdue_pagination(seeded):
    tag, ids = seeded
    j1 = overdue(tag, page_size=2).json()
    assert [t["id"] for t in j1["tasks"]] == [ids["old3"], ids["old2"]]
    assert j1["total_count"] == 3
    assert j1.get("next_page_token")
    j2 = overdue(tag, page_size=2, page_token=j1["next_page_token"]).json()
    assert [t["id"] for t in j2["tasks"]] == [ids["old1"]]
    assert not j2.get("next_page_token")


def test_overdue_status_filter(seeded):
    tag, ids = seeded
    requests.put(f"{API}/{ids['old1']}", json={"status": "in_progress"}, timeout=10)
    j = overdue(tag, status="in_progress").json()
    assert [t["id"] for t in j["tasks"]] == [ids["old1"]]


def test_overdue_assigned_to_filter(seeded):
    tag, ids = seeded
    requests.post(f"{API}/{ids['old2']}/assign", json={"email": "a@example.com"}, timeout=10)
    j = overdue(tag, assigned_to="a@example.com").json()
    assert [t["id"] for t in j["tasks"]] == [ids["old2"]]


def test_overdue_not_captured_by_id_route():
    r = requests.get(API + "/overdue", timeout=10)
    assert r.status_code == 200
    assert "tasks" in r.json()


def test_assign_happy_and_refetch(seeded):
    tag, ids = seeded
    r = requests.post(f"{API}/{ids['future']}/assign", json={"email": "bob@example.com"}, timeout=10)
    assert r.status_code == 200
    assert r.json()["assigned_to"] == "bob@example.com"
    g = requests.get(f"{API}/{ids['future']}", timeout=10)
    assert g.status_code == 200 and g.json()["assigned_to"] == "bob@example.com"


@pytest.mark.parametrize("body", [{"email": "not-an-email"}, {}, {"email": ""}])
def test_assign_invalid_email(seeded, body):
    tag, ids = seeded
    r = requests.post(f"{API}/{ids['future']}/assign", json=body, timeout=10)
    assert r.status_code == 400
    assert r.json()["error"]["code"] == "VALIDATION_ERROR"


def test_assign_malformed_body(seeded):
    tag, ids = seeded
    r = requests.post(f"{API}/{ids['future']}/assign", data="garbage",
                      headers={"Content-Type": "application/json"}, timeout=10)
    assert r.status_code == 400
    assert r.json()["error"]["code"] == "VALIDATION_ERROR"


def test_assign_unknown_task():
    r = requests.post(f"{API}/does-not-exist/assign", json={"email": "a@example.com"}, timeout=10)
    assert r.status_code == 404
    assert r.json()["error"]["code"] == "NOT_FOUND"


def test_regression_create_get_list_search_stats(seeded):
    tag, ids = seeded
    assert requests.get(f"{API}/{ids['nodue']}", timeout=10).json()["id"] == ids["nodue"]
    assert requests.get(f"{API}/nonexistent", timeout=10).status_code == 404
    j = requests.get(API, params={"tags": tag, "page_size": 2}, timeout=10).json()
    assert len(j["tasks"]) == 2 and j["total_count"] == 5 and j.get("next_page_token")
    assert requests.get(API + "/search", params={"q": "old3"}, timeout=10).status_code == 200
    assert requests.get(API + "/stats", timeout=10).status_code == 200


@pytest.mark.parametrize("ps", ["0", "101", "abc"])
def test_overdue_page_size_bounds(ps):
    r = requests.get(API + "/overdue", params={"page_size": ps}, timeout=10)
    assert r.status_code == 400
    assert r.json()["error"]["code"] == "VALIDATION_ERROR"


def test_overdue_page_size_max_ok():
    assert requests.get(API + "/overdue", params={"page_size": 100}, timeout=10).status_code == 200
