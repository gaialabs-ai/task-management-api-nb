import uuid
from datetime import datetime, timedelta, timezone
from conftest import call

def iso(days):
    return (datetime.now(timezone.utc) + timedelta(days=days)).strftime("%Y-%m-%dT%H:%M:%SZ")

def mk(title=None, due=None, **kw):
    p = {"title": title or f"ftrun_{uuid.uuid4().hex[:8]}"}
    if due is not None:
        p["due_date"] = due
    p.update(kw)
    r = call("POST", "/api/v1/tasks", json=p)
    assert r.status_code == 201, r.text
    return r.json()

def overdue_all():
    out, tok = [], None
    while True:
        q = "?page_size=50" + (f"&page_token={tok}" if tok else "")
        r = call("GET", "/api/v1/tasks/overdue" + q)
        assert r.status_code == 200
        b = r.json(); out += b["tasks"]; tok = b.get("next_page_token")
        if not tok: return out, b

def test_overdue_includes_past_excludes_others():
    past = mk(due=iso(-3)); fut = mk(due=iso(5)); nodue = mk()
    done = mk(due=iso(-4)); call("PATCH", f"/api/v1/tasks/{done['id']}/status", json={"status": "completed"})
    canc = mk(due=iso(-4)); call("PATCH", f"/api/v1/tasks/{canc['id']}/status", json={"status": "cancelled"})
    tasks, b = overdue_all()
    ids = {t["id"] for t in tasks}
    assert past["id"] in ids
    assert not ({fut["id"], nodue["id"], done["id"], canc["id"]} & ids)
    assert b["total_count"] >= 1

def test_overdue_sorted_asc_and_not_shadowed():
    mk(due=iso(-1)); mk(due=iso(-10)); mk(due=iso(-5))
    r = call("GET", "/api/v1/tasks/overdue")
    assert r.status_code == 200 and "tasks" in r.json()
    d = [t["due_date"] for t in r.json()["tasks"]]
    assert d == sorted(d) and len(d) >= 3

def test_overdue_pagination():
    for i in range(3): mk(due=iso(-20 - i))
    r1 = call("GET", "/api/v1/tasks/overdue?page_size=2")
    b1 = r1.json()
    assert r1.status_code == 200 and len(b1["tasks"]) == 2
    assert b1["next_page_token"] and b1["total_count"] >= 3
    r2 = call("GET", f"/api/v1/tasks/overdue?page_size=2&page_token={b1['next_page_token']}")
    assert r2.status_code == 200
    assert not ({t["id"] for t in b1["tasks"]} & {t["id"] for t in r2.json()["tasks"]})
    assert r2.json()["tasks"]

def test_overdue_invalid_page_size():
    r = call("GET", "/api/v1/tasks/overdue?page_size=abc")
    assert r.status_code == 400

def test_assign_success():
    t = mk()
    r = call("POST", f"/api/v1/tasks/{t['id']}/assign", json={"assigned_to": "ftrun@example.com"})
    assert r.status_code == 200 and r.json()["assigned_to"] == "ftrun@example.com"
    assert call("GET", f"/api/v1/tasks/{t['id']}").json()["assigned_to"] == "ftrun@example.com"

def test_assign_invalid_email():
    t = mk()
    r = call("POST", f"/api/v1/tasks/{t['id']}/assign", json={"assigned_to": "not-an-email"})
    assert r.status_code == 400 and r.json()["error"]["code"] == "VALIDATION_ERROR"

def test_assign_missing_field():
    t = mk()
    r = call("POST", f"/api/v1/tasks/{t['id']}/assign", json={})
    assert r.status_code == 400

def test_assign_unknown_task():
    r = call("POST", "/api/v1/tasks/ftrun-nonexistent/assign", json={"assigned_to": "a@example.com"})
    assert r.status_code == 404 and r.json()["error"]["code"] == "NOT_FOUND"

def test_list_tasks_pagination_stable():
    for _ in range(3): mk()
    r1 = call("GET", "/api/v1/tasks?page_size=2"); b1 = r1.json()
    assert r1.status_code == 200 and len(b1["tasks"]) == 2 and b1["next_page_token"]
    r1b = call("GET", "/api/v1/tasks?page_size=2")
    assert [t["id"] for t in r1b.json()["tasks"]] == [t["id"] for t in b1["tasks"]]
    r2 = call("GET", f"/api/v1/tasks?page_size=2&page_token={b1['next_page_token']}")
    assert r2.status_code == 200 and r2.json()["tasks"]
    assert not ({t["id"] for t in b1["tasks"]} & {t["id"] for t in r2.json()["tasks"]})
    assert b1["total_count"] >= 3

def test_crud_lifecycle():
    t = mk(description="d", priority="high")
    assert call("GET", f"/api/v1/tasks/{t['id']}").status_code == 200
    r = call("PUT", f"/api/v1/tasks/{t['id']}", json={"title": "ftrun_updated"})
    assert r.status_code == 200 and r.json()["title"] == "ftrun_updated"
    r = call("PATCH", f"/api/v1/tasks/{t['id']}/status", json={"status": "in_progress"})
    assert r.status_code == 200 and r.json()["status"] == "in_progress"
    assert call("DELETE", f"/api/v1/tasks/{t['id']}").status_code in (200, 204)
    assert call("GET", f"/api/v1/tasks/{t['id']}").status_code == 404

def test_create_validation_and_get_not_found():
    assert call("POST", "/api/v1/tasks", json={"description": "x"}).status_code == 400
    assert call("GET", "/api/v1/tasks/ftrun-missing").status_code == 404

def test_search_and_stats():
    mk(title="ftrun_searchable_zebra")
    r = call("GET", "/api/v1/tasks/search?q=ftrun_searchable_zebra")
    assert r.status_code == 200 and r.json()["total_count"] >= 1
    r = call("GET", "/api/v1/tasks/stats")
    assert r.status_code == 200 and r.json()["total"] >= 1

def test_comments():
    t = mk()
    r = call("POST", f"/api/v1/tasks/{t['id']}/comments", json={"body": "hi", "author": "ftrun"})
    assert r.status_code in (200, 201), r.text
    r = call("GET", f"/api/v1/tasks/{t['id']}/comments")
    assert r.status_code == 200
