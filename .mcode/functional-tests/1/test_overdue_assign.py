import os
import uuid
from datetime import datetime, timedelta, timezone

import pytest
import requests

BASE = "http://localhost:%s" % os.environ.get("PORT", "8080")
API = BASE + "/api/v1/tasks"
T = 10
SORT = "created_at_asc"  # default order is unsorted (map iteration), so pin an order


def iso(d):
    return d.strftime("%Y-%m-%dT%H:%M:%SZ")


@pytest.fixture(scope="session", autouse=True)
def healthy():
    assert requests.get(BASE + "/health", timeout=T).status_code == 200


@pytest.fixture(scope="module")
def seeded():
    run = "ftrun_" + uuid.uuid4().hex[:8]
    now = datetime.now(timezone.utc)
    ids = {}
    spec = [
        ("old", now - timedelta(days=10), None),
        ("mid", now - timedelta(days=5), None),
        ("new", now - timedelta(days=1), None),
        ("done", now - timedelta(days=7), "completed"),
        ("cancelled", now - timedelta(days=6), "cancelled"),
        ("future", now + timedelta(days=5), None),
        ("nodue", None, None),
    ]
    for name, due, status in spec:
        body = {"title": f"{run}_{name}"}
        if due:
            body["due_date"] = iso(due)
        r = requests.post(API, json=body, timeout=T)
        assert r.status_code == 201, r.text
        tid = r.json()["id"]
        ids[name] = tid
        if status:
            r = requests.patch(f"{API}/{tid}/status", json={"status": status}, timeout=T)
            assert r.status_code == 200, r.text
    yield ids
    for tid in ids.values():
        requests.delete(f"{API}/{tid}", timeout=T)


def all_overdue():
    out, token = [], ""
    while True:
        params = {"page_size": 100}
        if token:
            params["page_token"] = token
        r = requests.get(API + "/overdue", params=params, timeout=T)
        assert r.status_code == 200, r.text
        j = r.json()
        out += j["tasks"]
        token = j.get("next_page_token", "")
        if not token:
            return out


class TestOverdue:
    def test_overdue_filter_and_order(self, seeded):
        tasks = all_overdue()
        got = [t["id"] for t in tasks if t["id"] in seeded.values()]
        assert got == [seeded["old"], seeded["mid"], seeded["new"]]
        dues = [t["due_date"] for t in tasks]
        assert dues == sorted(dues)
        for t in tasks:
            assert t["status"] not in ("completed", "cancelled")
            assert t.get("due_date")

    def test_overdue_pagination(self, seeded):
        r = requests.get(API + "/overdue", params={"page_size": 1}, timeout=T)
        assert r.status_code == 200
        j = r.json()
        assert len(j["tasks"]) == 1
        assert j["next_page_token"]
        seen = [j["tasks"][0]["id"]]
        token = j["next_page_token"]
        while token:
            j = requests.get(API + "/overdue", params={"page_size": 1, "page_token": token}, timeout=T).json()
            seen += [t["id"] for t in j["tasks"]]
            token = j.get("next_page_token", "")
        assert len(seen) == len(set(seen))
        assert [i for i in seen if i in seeded.values()] == [seeded["old"], seeded["mid"], seeded["new"]]

    def test_overdue_route_precedence_over_id(self, seeded):
        r = requests.get(API + "/overdue", timeout=T)
        assert r.status_code == 200
        assert "tasks" in r.json()

    def test_overdue_invalid_page_size(self):
        r = requests.get(API + "/overdue", params={"page_size": "abc"}, timeout=T)
        assert r.status_code == 400

    def test_overdue_nonnumeric_token_starts_at_zero(self, seeded):
        # paginate() contract: non-numeric token is treated as offset 0
        a = requests.get(API + "/overdue", params={"page_token": "!!x"}, timeout=T)
        b = requests.get(API + "/overdue", timeout=T)
        assert a.status_code == 200
        assert a.json() == b.json()


class TestAssign:
    def test_assign_happy(self, seeded):
        r = requests.post(f"{API}/{seeded['future']}/assign", json={"assigned_to": "alice@example.com"}, timeout=T)
        assert r.status_code == 200, r.text
        assert r.json()["assigned_to"] == "alice@example.com"
        assert r.json()["id"] == seeded["future"]
        g = requests.get(f"{API}/{seeded['future']}", timeout=T).json()
        assert g["assigned_to"] == "alice@example.com"

    @pytest.mark.parametrize("email", ["not-an-email", "", "a@", "@b.com"])
    def test_assign_bad_email(self, seeded, email):
        r = requests.post(f"{API}/{seeded['nodue']}/assign", json={"assigned_to": email}, timeout=T)
        assert r.status_code == 400, r.text
        assert r.json()["error"]["code"] == "VALIDATION_ERROR"

    def test_assign_bad_body(self, seeded):
        r = requests.post(f"{API}/{seeded['nodue']}/assign", data="{bad", headers={"Content-Type": "application/json"}, timeout=T)
        assert r.status_code == 400
        assert r.json()["error"]["code"] == "VALIDATION_ERROR"

    def test_assign_missing_field(self, seeded):
        r = requests.post(f"{API}/{seeded['nodue']}/assign", json={}, timeout=T)
        assert r.status_code == 400
        assert r.json()["error"]["code"] == "VALIDATION_ERROR"

    def test_assign_unknown_task(self):
        r = requests.post(f"{API}/does-not-exist-{uuid.uuid4().hex}/assign", json={"assigned_to": "a@b.com"}, timeout=T)
        assert r.status_code == 404
        assert r.json()["error"]["code"] == "NOT_FOUND"


class TestListRegression:
    def test_list_pagination(self, seeded):
        r = requests.get(API, params={"page_size": 2, "sort_order": SORT}, timeout=T)
        assert r.status_code == 200
        j = r.json()
        assert len(j["tasks"]) == 2
        assert j["next_page_token"]
        r2 = requests.get(API, params={"page_size": 2, "sort_order": SORT, "page_token": j["next_page_token"]}, timeout=T)
        assert r2.status_code == 200
        ids1 = {t["id"] for t in j["tasks"]}
        assert not ids1 & {t["id"] for t in r2.json()["tasks"]}

    def test_list_walk_covers_total(self, seeded):
        seen, token, total = [], "", None
        while True:
            p = {"page_size": 3, "sort_order": SORT}
            if token:
                p["page_token"] = token
            j = requests.get(API, params=p, timeout=T).json()
            total = j["total_count"]
            seen += [t["id"] for t in j["tasks"]]
            token = j.get("next_page_token", "")
            if not token:
                break
        assert len(seen) == len(set(seen)) == total

    def test_get_by_id_and_404(self, seeded):
        assert requests.get(f"{API}/{seeded['old']}", timeout=T).status_code == 200
        assert requests.get(f"{API}/nope-{uuid.uuid4().hex}", timeout=T).status_code == 404
