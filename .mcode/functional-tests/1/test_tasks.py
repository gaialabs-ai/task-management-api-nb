import uuid
from datetime import datetime, timedelta, timezone

T = "/api/v1/tasks"

def iso(d): return d.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
NOW = datetime.now(timezone.utc)

def mk(api, title=None, due=None, **kw):
    body = {"title": title or "ft_" + uuid.uuid4().hex[:8], **kw}
    if due is not None: body["due_date"] = iso(due)
    r = api.req("POST", T, json=body)
    assert r.status_code == 201, r.text
    return r.json()

def all_pages(api, path, size=100, **extra):
    out, tok = [], None
    while True:
        p = {"page_size": size, **extra}
        if tok: p["page_token"] = tok
        r = api.req("GET", path, params=p)
        assert r.status_code == 200, r.text
        j = r.json(); out += j["tasks"]; tok = j.get("next_page_token")
        if not tok: return out, j

class TestOverdue:
    def test_overdue_happy_path_filters_and_order(self, api):
        a = mk(api, due=NOW - timedelta(days=3))
        b = mk(api, due=NOW - timedelta(days=10))
        fut = mk(api, due=NOW + timedelta(days=5))
        nod = mk(api)
        done = mk(api, due=NOW - timedelta(days=2))
        canc = mk(api, due=NOW - timedelta(days=2))
        assert api.req("PATCH", f"{T}/{done['id']}/status", json={"status": "completed"}).status_code == 200
        assert api.req("PATCH", f"{T}/{canc['id']}/status", json={"status": "cancelled"}).status_code == 200
        tasks, last = all_pages(api, T + "/overdue")
        ids = [t["id"] for t in tasks]
        assert a["id"] in ids and b["id"] in ids
        for x in (fut, nod, done, canc): assert x["id"] not in ids
        assert ids.index(b["id"]) < ids.index(a["id"])
        dues = [t["due_date"] for t in tasks]
        assert dues == sorted(dues)
        assert all(t["status"] not in ("completed", "cancelled") for t in tasks)

    def test_overdue_response_shape(self, api):
        mk(api, due=NOW - timedelta(days=1))
        r = api.req("GET", T + "/overdue")
        assert r.status_code == 200
        j = r.json()
        assert {"tasks", "total_count"} <= set(j)
        assert j["total_count"] >= 1 and len(j["tasks"]) <= 20

    def test_overdue_pagination(self, api):
        for i in range(3): mk(api, due=NOW - timedelta(days=30 + i))
        r = api.req("GET", T + "/overdue", params={"page_size": 2})
        j = r.json()
        assert r.status_code == 200 and len(j["tasks"]) == 2 and j["next_page_token"]
        r2 = api.req("GET", T + "/overdue", params={"page_size": 2, "page_token": j["next_page_token"]})
        j2 = r2.json()
        assert r2.status_code == 200
        assert not {t["id"] for t in j["tasks"]} & {t["id"] for t in j2["tasks"]}
        assert j2["total_count"] == j["total_count"]

    def test_overdue_page_size_too_large(self, api):
        assert api.req("GET", T + "/overdue", params={"page_size": 101}).status_code == 400

    def test_overdue_page_size_zero_negative(self, api):
        assert api.req("GET", T + "/overdue", params={"page_size": -1}).status_code == 400

    def test_overdue_invalid_page_token(self, api):
        r = api.req("GET", T + "/overdue", params={"page_token": "abc"})
        assert r.status_code in (200, 400), r.text

    def test_overdue_not_shadowed_by_id_route(self, api):
        r = api.req("GET", T + "/overdue")
        assert r.status_code == 200 and "tasks" in r.json()

class TestAssign:
    def test_assign_happy_path(self, api):
        t = mk(api)
        r = api.req("POST", f"{T}/{t['id']}/assign", json={"assigned_to": "alice@example.com"})
        assert r.status_code == 200, r.text
        j = r.json()
        assert j["id"] == t["id"] and j["assigned_to"] == "alice@example.com"
        assert j["updated_at"] > t["updated_at"]
        g = api.req("GET", f"{T}/{t['id']}")
        assert g.json()["assigned_to"] == "alice@example.com"

    def test_assign_reassign(self, api):
        t = mk(api, assigned_to="a@example.com")
        r = api.req("POST", f"{T}/{t['id']}/assign", json={"assigned_to": "b@example.com"})
        assert r.status_code == 200 and r.json()["assigned_to"] == "b@example.com"

    def test_assign_invalid_email(self, api):
        t = mk(api)
        r = api.req("POST", f"{T}/{t['id']}/assign", json={"assigned_to": "not-an-email"})
        assert r.status_code == 400, r.text

    def test_assign_missing_field(self, api):
        t = mk(api)
        assert api.req("POST", f"{T}/{t['id']}/assign", json={}).status_code == 400

    def test_assign_unknown_task(self, api):
        r = api.req("POST", f"{T}/{uuid.uuid4()}/assign", json={"assigned_to": "a@example.com"})
        assert r.status_code == 404, r.text

class TestListAndHealth:
    def test_health(self, api):
        r = api.req("GET", "/health")
        assert r.status_code == 200 and r.json()["status"] == "healthy"

    def test_list_default_and_shape(self, api):
        mk(api)
        r = api.req("GET", T)
        j = r.json()
        assert r.status_code == 200 and {"tasks", "total_count"} <= set(j) and len(j["tasks"]) <= 20

    def test_list_pagination_walk_no_duplicates(self, api):
        for _ in range(5): mk(api)
        tasks, last = all_pages(api, T, size=2)
        ids = [t["id"] for t in tasks]
        assert len(ids) == len(set(ids)) == last["total_count"]

    def test_list_pagination_walk_sorted_no_duplicates(self, api):
        for _ in range(5): mk(api)
        tasks, last = all_pages(api, T, size=2, sort_order="created_at_asc")
        ids = [t["id"] for t in tasks]
        assert len(ids) == len(set(ids)) == last["total_count"]

    def test_list_page_size_bounds(self, api):
        assert api.req("GET", T, params={"page_size": 100}).status_code == 200
        assert api.req("GET", T, params={"page_size": 101}).status_code == 400

    def test_list_page_size_negative(self, api):
        assert api.req("GET", T, params={"page_size": -5}).status_code == 400

    def test_list_invalid_page_token(self, api):
        r = api.req("GET", T, params={"page_token": "abc"})
        assert r.status_code in (200, 400), r.text
