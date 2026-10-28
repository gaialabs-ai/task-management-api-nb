import json, uuid, datetime as dt, requests, pytest

BASE = "http://localhost:8080/api/v1"
LOG = "/tmp/ft_log.jsonl"
RUN = "ftrun_" + uuid.uuid4().hex[:8]


def rec(name, method, endpoint, body, r):
    with open(LOG, "a") as f:
        f.write(json.dumps({"name": name, "method": method, "endpoint": endpoint,
                            "body": body, "status": r.status_code, "out": r.text[:3500]}) + "\n")


def iso(days):
    return (dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=days)).strftime("%Y-%m-%dT%H:%M:%SZ")


def mk(title, due=None, status=None):
    body = {"title": f"{RUN}_{title}"}
    if due:
        body["due_date"] = due
    r = requests.post(f"{BASE}/tasks", json=body, timeout=10)
    assert r.status_code == 201, r.text
    t = r.json()
    if status:
        u = requests.put(f"{BASE}/tasks/{t['id']}", json={"status": status}, timeout=10)
        assert u.status_code == 200, u.text
        t = u.json()
    return t


def overdue_all(size=2):
    out, tok, pages = [], "", 0
    while True:
        p = {"page_size": size}
        if tok:
            p["page_token"] = tok
        r = requests.get(f"{BASE}/tasks/overdue", params=p, timeout=10)
        assert r.status_code == 200, r.text
        j = r.json()
        out += j["tasks"]
        pages += 1
        tok = j.get("next_page_token", "")
        if not tok:
            return out, pages


@pytest.fixture(scope="module", autouse=True)
def health():
    assert requests.get("http://localhost:8080/health", timeout=5).status_code == 200


@pytest.fixture(scope="module")
def data():
    d = {}
    d["old"] = mk("old", iso(-30))
    d["older"] = mk("older", iso(-60))
    d["same_a"] = mk("same_a", "2001-01-01T00:00:00Z")
    d["same_b"] = mk("same_b", "2001-01-01T00:00:00Z")
    d["future"] = mk("future", iso(30))
    d["nodue"] = mk("nodue")
    d["done"] = mk("done", iso(-10), "completed")
    d["cancelled"] = mk("cancelled", iso(-10), "cancelled")
    d["inprog"] = mk("inprog", iso(-5), "in_progress")
    return d


def mine(tasks):
    return [t for t in tasks if t["title"].startswith(RUN)]


def test_overdue_filtering(data):
    r = requests.get(f"{BASE}/tasks/overdue", params={"page_size": 100}, timeout=10)
    rec("test_overdue_filtering", "GET", "/api/v1/tasks/overdue?page_size=100", None, r)
    assert r.status_code == 200
    j = r.json()
    assert "total_count" in j and j["total_count"] >= len(j["tasks"])
    ids = {t["id"] for t in mine(j["tasks"])}
    assert data["old"]["id"] in ids and data["older"]["id"] in ids and data["inprog"]["id"] in ids
    for k in ("future", "nodue", "done", "cancelled"):
        assert data[k]["id"] not in ids, k


def test_overdue_sorted_with_tiebreak(data):
    tasks, _ = overdue_all(size=100)
    m = mine(tasks)
    keys = [(t["due_date"], t["id"]) for t in m]
    assert keys == sorted(keys)
    r = requests.get(f"{BASE}/tasks/overdue", params={"page_size": 100}, timeout=10)
    rec("test_overdue_sorted_with_tiebreak", "GET", "/api/v1/tasks/overdue?page_size=100", None, r)
    assert r.status_code == 200
    pos = [t["id"] for t in r.json()["tasks"]]
    assert pos.index(data["same_a"]["id"]) != pos.index(data["same_b"]["id"])


def test_overdue_pagination(data):
    r = requests.get(f"{BASE}/tasks/overdue", params={"page_size": 2}, timeout=10)
    rec("test_overdue_pagination", "GET", "/api/v1/tasks/overdue?page_size=2", None, r)
    assert r.status_code == 200
    j = r.json()
    assert len(j["tasks"]) == 2 and j["next_page_token"]
    tasks, pages = overdue_all(size=2)
    ids = [t["id"] for t in tasks]
    assert len(ids) == len(set(ids)) == j["total_count"]
    assert pages > 1
    keys = [(t["due_date"], t["id"]) for t in tasks]
    assert keys == sorted(keys)


def test_overdue_not_captured_by_id():
    r = requests.get(f"{BASE}/tasks/overdue", timeout=10)
    rec("test_overdue_not_captured_by_id", "GET", "/api/v1/tasks/overdue", None, r)
    assert r.status_code == 200 and "tasks" in r.json()


def test_overdue_page_size_too_large():
    r = requests.get(f"{BASE}/tasks/overdue", params={"page_size": 101}, timeout=10)
    rec("test_overdue_page_size_too_large", "GET", "/api/v1/tasks/overdue?page_size=101", None, r)
    assert r.status_code == 400


def test_list_tasks_default_order_pagination_consistent(data):
    r = requests.get(f"{BASE}/tasks", params={"page_size": 3}, timeout=10)
    rec("test_list_tasks_default_order_pagination_consistent", "GET", "/api/v1/tasks?page_size=3", None, r)
    assert r.status_code == 200
    j = r.json()
    assert len(j["tasks"]) == 3 and j["next_page_token"] and j["total_count"] >= 9
    seen, tok = [], ""
    while True:
        p = {"page_size": 4}
        if tok:
            p["page_token"] = tok
        jj = requests.get(f"{BASE}/tasks", params=p, timeout=10).json()
        seen += [t["id"] for t in jj["tasks"]]
        tok = jj.get("next_page_token", "")
        if not tok:
            break
    assert len(seen) == len(set(seen)) == jj["total_count"]


def test_list_tasks_page_size_too_large():
    r = requests.get(f"{BASE}/tasks", params={"page_size": 101}, timeout=10)
    rec("test_list_tasks_page_size_too_large", "GET", "/api/v1/tasks?page_size=101", None, r)
    assert r.status_code == 400


def test_assign_happy(data):
    body = {"assigned_to": "alice@example.com"}
    r = requests.post(f"{BASE}/tasks/{data['old']['id']}/assign", json=body, timeout=10)
    rec("test_assign_happy", "POST", f"/api/v1/tasks/{data['old']['id']}/assign", body, r)
    assert r.status_code == 200
    assert r.json()["assigned_to"] == "alice@example.com" and r.json()["id"] == data["old"]["id"]
    g = requests.get(f"{BASE}/tasks/{data['old']['id']}", timeout=10).json()
    assert g["assigned_to"] == "alice@example.com"


def test_assign_reassign(data):
    tid = data["older"]["id"]
    requests.post(f"{BASE}/tasks/{tid}/assign", json={"assigned_to": "a@example.com"}, timeout=10)
    body = {"assigned_to": "b@example.com"}
    r = requests.post(f"{BASE}/tasks/{tid}/assign", json=body, timeout=10)
    rec("test_assign_reassign", "POST", f"/api/v1/tasks/{tid}/assign", body, r)
    assert r.status_code == 200 and r.json()["assigned_to"] == "b@example.com"


@pytest.mark.parametrize("val", ["not-an-email", "", "a@", "@b.com"])
def test_assign_invalid_email(data, val):
    body = {"assigned_to": val}
    tid = data["future"]["id"]
    r = requests.post(f"{BASE}/tasks/{tid}/assign", json=body, timeout=10)
    rec(f"test_assign_invalid_email[{val}]", "POST", f"/api/v1/tasks/{tid}/assign", body, r)
    assert r.status_code == 400


def test_assign_missing_field(data):
    tid = data["future"]["id"]
    r = requests.post(f"{BASE}/tasks/{tid}/assign", json={}, timeout=10)
    rec("test_assign_missing_field", "POST", f"/api/v1/tasks/{tid}/assign", {}, r)
    assert r.status_code == 400


def test_assign_malformed_json(data):
    tid = data["future"]["id"]
    r = requests.post(f"{BASE}/tasks/{tid}/assign", data="{bad", headers={"Content-Type": "application/json"}, timeout=10)
    rec("test_assign_malformed_json", "POST", f"/api/v1/tasks/{tid}/assign", "{bad", r)
    assert r.status_code == 400


def test_assign_unknown_task():
    body = {"assigned_to": "alice@example.com"}
    r = requests.post(f"{BASE}/tasks/{uuid.uuid4()}/assign", json=body, timeout=10)
    rec("test_assign_unknown_task", "POST", "/api/v1/tasks/<unknown-uuid>/assign", body, r)
    assert r.status_code == 404


def test_assign_unchanged_on_invalid(data):
    tid = data["nodue"]["id"]
    requests.post(f"{BASE}/tasks/{tid}/assign", json={"assigned_to": "keep@example.com"}, timeout=10)
    r = requests.post(f"{BASE}/tasks/{tid}/assign", json={"assigned_to": "bad"}, timeout=10)
    assert r.status_code == 400
    g = requests.get(f"{BASE}/tasks/{tid}", timeout=10)
    rec("test_assign_unchanged_on_invalid", "GET", f"/api/v1/tasks/{tid}", None, g)
    assert g.json()["assigned_to"] == "keep@example.com"


def test_list_tasks_sorted_pagination(data):
    r = requests.get(f"{BASE}/tasks", params={"page_size": 3, "sort_order": "created_at_asc"}, timeout=10)
    rec("test_list_tasks_sorted_pagination", "GET", "/api/v1/tasks?page_size=3&sort_order=created_at_asc", None, r)
    assert r.status_code == 200
    j = r.json()
    assert len(j["tasks"]) == 3 and j["next_page_token"]
    seen, tok = [], ""
    while True:
        p = {"page_size": 4, "sort_order": "created_at_asc"}
        if tok:
            p["page_token"] = tok
        jj = requests.get(f"{BASE}/tasks", params=p, timeout=10).json()
        seen += [t["id"] for t in jj["tasks"]]
        tok = jj.get("next_page_token", "")
        if not tok:
            break
    assert len(seen) == len(set(seen)) == jj["total_count"]
