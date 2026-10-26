import uuid, datetime as dt
import pytest, requests

B = "http://localhost:8080"
J = {"Content-Type": "application/json"}
T = 10


def iso(d):
    return d.strftime("%Y-%m-%dT%H:%M:%SZ")


def mk(title, due=None, **kw):
    body = {"title": title, **kw}
    if due:
        body["due_date"] = iso(due)
    r = requests.post(f"{B}/api/v1/tasks", json=body, timeout=T)
    assert r.status_code == 201, r.text
    return r.json()


def all_overdue():
    out, tok = [], None
    while True:
        p = {"page_size": 2}
        if tok:
            p["page_token"] = tok
        r = requests.get(f"{B}/api/v1/tasks/overdue", params=p, timeout=T)
        assert r.status_code == 200, r.text
        d = r.json()
        out += d["tasks"]
        tok = d.get("next_page_token")
        if not tok:
            return out, d


@pytest.fixture(scope="module", autouse=True)
def health():
    assert requests.get(f"{B}/health", timeout=T).status_code == 200


@pytest.fixture(scope="module")
def seeded():
    run = "ftrun_" + uuid.uuid4().hex[:8]
    now = dt.datetime.utcnow()
    t = {}
    t["old"] = mk(f"{run}_old", now - dt.timedelta(days=10))
    t["newer"] = mk(f"{run}_newer", now - dt.timedelta(days=2))
    t["done"] = mk(f"{run}_done", now - dt.timedelta(days=5))
    t["canc"] = mk(f"{run}_canc", now - dt.timedelta(days=5))
    t["future"] = mk(f"{run}_future", now + dt.timedelta(days=5))
    t["nodue"] = mk(f"{run}_nodue")
    for k, s in (("done", "completed"), ("canc", "cancelled")):
        r = requests.patch(f"{B}/api/v1/tasks/{t[k]['id']}/status", json={"status": s}, timeout=T)
        assert r.status_code == 200, r.text
    yield t
    for x in t.values():
        requests.delete(f"{B}/api/v1/tasks/{x['id']}", timeout=T)


def test_overdue_filter_and_sort(seeded):
    tasks, _ = all_overdue()
    ids = [x["id"] for x in tasks]
    assert seeded["old"]["id"] in ids and seeded["newer"]["id"] in ids
    for k in ("done", "canc", "future", "nodue"):
        assert seeded[k]["id"] not in ids
    assert ids.index(seeded["old"]["id"]) < ids.index(seeded["newer"]["id"])
    dues = [x["due_date"] for x in tasks]
    assert dues == sorted(dues)


def test_overdue_shape_and_pagination(seeded):
    r = requests.get(f"{B}/api/v1/tasks/overdue", params={"page_size": 1}, timeout=T)
    assert r.status_code == 200
    d = r.json()
    assert len(d["tasks"]) == 1 and d["total_count"] >= 2 and d.get("next_page_token")
    r2 = requests.get(f"{B}/api/v1/tasks/overdue", params={"page_size": 1, "page_token": d["next_page_token"]}, timeout=T)
    assert r2.status_code == 200
    assert r2.json()["tasks"][0]["id"] != d["tasks"][0]["id"]


def test_overdue_not_shadowed_by_id():
    r = requests.get(f"{B}/api/v1/tasks/overdue", timeout=T)
    assert r.status_code == 200 and "tasks" in r.json()


def test_overdue_invalid_page_token():
    # same lenient behavior as GET /api/v1/tasks (shared pagination helper)
    a = requests.get(f"{B}/api/v1/tasks/overdue", params={"page_token": "zzz"}, timeout=T)
    b = requests.get(f"{B}/api/v1/tasks", params={"page_token": "zzz"}, timeout=T)
    assert a.status_code == b.status_code == 200


def test_assign_success(seeded):
    r = requests.post(f"{B}/api/v1/tasks/{seeded['old']['id']}/assign", json={"assigned_to": "alice@example.com"}, timeout=T)
    assert r.status_code == 200, r.text
    assert r.json()["assigned_to"] == "alice@example.com"
    g = requests.get(f"{B}/api/v1/tasks/{seeded['old']['id']}", timeout=T).json()
    assert g["assigned_to"] == "alice@example.com"


@pytest.mark.parametrize("body", [{"assigned_to": "not-an-email"}, {}, {"assigned_to": ""}])
def test_assign_invalid_email(seeded, body):
    r = requests.post(f"{B}/api/v1/tasks/{seeded['newer']['id']}/assign", json=body, timeout=T)
    assert r.status_code == 400, r.text
    assert r.json()["error"]["code"] == "VALIDATION_ERROR"


def test_assign_unknown_task():
    r = requests.post(f"{B}/api/v1/tasks/does-not-exist/assign", json={"assigned_to": "a@b.com"}, timeout=T)
    assert r.status_code == 404
    assert r.json()["error"]["code"] == "NOT_FOUND"


def test_list_tasks_pagination_unchanged(seeded):
    r = requests.get(f"{B}/api/v1/tasks", params={"page_size": 2}, timeout=T)
    assert r.status_code == 200
    d = r.json()
    assert len(d["tasks"]) == 2 and d["total_count"] >= 6 and d["next_page_token"]
    r2 = requests.get(f"{B}/api/v1/tasks", params={"page_size": 2, "page_token": d["next_page_token"]}, timeout=T)
    assert r2.status_code == 200
    assert {x["id"] for x in d["tasks"]}.isdisjoint({x["id"] for x in r2.json()["tasks"]})


def test_existing_endpoints(seeded):
    assert requests.get(f"{B}/api/v1/tasks/{seeded['old']['id']}", timeout=T).status_code == 200
    assert requests.get(f"{B}/api/v1/tasks/stats", timeout=T).status_code == 200
    r = requests.get(f"{B}/api/v1/tasks/search", params={"q": seeded["old"]["title"]}, timeout=T)
    assert r.status_code == 200, r.text
    assert any(x["id"] == seeded["old"]["id"] for x in r.json()["tasks"])
    assert requests.get(f"{B}/api/v1/tasks/nonexistent-id", timeout=T).status_code == 404
