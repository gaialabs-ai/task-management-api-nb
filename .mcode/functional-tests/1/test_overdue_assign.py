import uuid, requests, pytest
B = "http://localhost:8080/api/v1/tasks"
RUN = uuid.uuid4().hex[:6]

def mk(title, due=None, **kw):
    body = {"title": f"ftrun_{RUN}_{title}", **kw}
    if due: body["due_date"] = due
    r = requests.post(B, json=body, timeout=10)
    assert r.status_code == 201, r.text
    return r.json()

def overdue_all():
    out, tok = [], ""
    while True:
        r = requests.get(B + "/overdue", params={"page_size": 100, "page_token": tok}, timeout=10)
        assert r.status_code == 200
        j = r.json(); out += j["tasks"]; tok = j.get("next_page_token", "")
        if not tok: return out

@pytest.fixture(scope="module", autouse=True)
def data():
    d = {}
    d["old"] = mk("old", "2019-01-01T00:00:00Z")
    d["older"] = mk("older", "2018-01-01T00:00:00Z")
    d["future"] = mk("future", "2099-01-01T00:00:00Z")
    d["nodue"] = mk("nodue")
    d["done"] = mk("done", "2017-01-01T00:00:00Z")
    d["canc"] = mk("canc", "2017-01-01T00:00:00Z")
    for k, s in (("done", "completed"), ("canc", "cancelled")):
        r = requests.put(f"{B}/{d[k]['id']}", json={"status": s}, timeout=10)
        assert r.status_code == 200, r.text
    yield d
    for t in d.values():
        requests.delete(f"{B}/{t['id']}", timeout=10)

def test_overdue_filter(data):
    ids = [t["id"] for t in overdue_all()]
    assert data["old"]["id"] in ids and data["older"]["id"] in ids
    for k in ("future", "nodue", "done", "canc"):
        assert data[k]["id"] not in ids

def test_overdue_sorted(data):
    ts = overdue_all()
    dues = [t["due_date"] for t in ts]
    assert dues == sorted(dues)
    ids = [t["id"] for t in ts]
    assert ids.index(data["older"]["id"]) < ids.index(data["old"]["id"])

def test_overdue_pagination():
    r = requests.get(B + "/overdue", params={"page_size": 1}, timeout=10)
    j = r.json()
    assert r.status_code == 200 and len(j["tasks"]) == 1 and j["total_count"] >= 2 and j["next_page_token"]
    r2 = requests.get(B + "/overdue", params={"page_size": 1, "page_token": j["next_page_token"]}, timeout=10)
    assert r2.status_code == 200 and r2.json()["tasks"][0]["id"] != j["tasks"][0]["id"]

@pytest.mark.parametrize("ps", ["0", "101", "abc", "-1"])
def test_overdue_bad_page_size(ps):
    r = requests.get(B + "/overdue", params={"page_size": ps}, timeout=10)
    assert r.status_code == 400 and r.json()["error"]["code"] == "VALIDATION_ERROR"

def test_overdue_route_precedence():
    r = requests.get(B + "/overdue", timeout=10)
    assert r.status_code == 200 and "tasks" in r.json()

def test_list_tasks_unchanged(data):
    r = requests.get(B, params={"page_size": 2}, timeout=10)
    j = r.json()
    assert r.status_code == 200 and len(j["tasks"]) == 2 and j["next_page_token"] and j["total_count"] >= 6

def test_assign_ok(data):
    r = requests.post(f"{B}/{data['old']['id']}/assign", json={"assigned_to": "bob@example.com"}, timeout=10)
    assert r.status_code == 200
    assert r.json()["assigned_to"] == "bob@example.com" and r.json()["id"] == data["old"]["id"]
    g = requests.get(f"{B}/{data['old']['id']}", timeout=10).json()
    assert g["assigned_to"] == "bob@example.com"

@pytest.mark.parametrize("v", ["not-an-email", "Bob <bob@example.com>", "@example.com"])
def test_assign_invalid_email(data, v):
    r = requests.post(f"{B}/{data['old']['id']}/assign", json={"assigned_to": v}, timeout=10)
    assert r.status_code == 400 and r.json()["error"]["code"] == "VALIDATION_ERROR"

def test_assign_missing_field(data):
    r = requests.post(f"{B}/{data['old']['id']}/assign", json={}, timeout=10)
    assert r.status_code == 400 and r.json()["error"]["code"] == "VALIDATION_ERROR"

def test_assign_unknown_task():
    r = requests.post(f"{B}/nonexistent-{RUN}/assign", json={"assigned_to": "bob@example.com"}, timeout=10)
    assert r.status_code == 404 and r.json()["error"]["code"] == "NOT_FOUND"
