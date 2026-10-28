"""GET /api/v1/tasks/overdue functional tests (live server).

Overdue rule (spec): due_date set AND strictly before now AND status not in
{completed, cancelled}. Sorted by due_date asc, id asc tiebreak. Offset
pagination with page_size (default 20, 1..100) and page_token.

Overdue data is seeded by creating tasks with past due dates through
POST /api/v1/tasks (there is no API to make a task overdue directly).
The sample tasks seeded by the server have no due date, so after each test's
teardown the overdue set is empty again.
"""
from datetime import datetime, timedelta, timezone

import requests

from conftest import API, TIMEOUT, record

OVERDUE = API + "/overdue"


def iso(dt):
    return dt.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def parse(ts):
    return datetime.fromisoformat(ts.replace("Z", "+00:00"))


class TestOverdueEmpty:
    def test_overdue_empty_returns_empty_array(self, tasks):
        # Only non-overdue tasks exist: future-dated, no due date, completed past, cancelled past
        tasks.create("future", due_date=iso(datetime.now(timezone.utc) + timedelta(days=3)))
        tasks.create("nodue")
        done = tasks.create("donepast", due_date="2020-01-01T00:00:00Z")
        tasks.set_status(done["id"], "completed")
        canc = tasks.create("cancpast", due_date="2020-01-01T00:00:00Z")
        tasks.set_status(canc["id"], "cancelled")

        resp = record(requests.get(OVERDUE, timeout=TIMEOUT))
        assert resp.status_code == 200
        body = resp.json()
        assert body["tasks"] == []  # must serialize as [] not null
        assert body["total_count"] == 0
        assert "next_page_token" not in body


class TestCreateWithPastDueDate:
    def test_create_task_accepts_past_due_date(self, tasks):
        payload = {"title": "ftrun_pastdue_create", "due_date": "2021-06-15T10:00:00Z"}
        resp = record(requests.post(API, json=payload, timeout=TIMEOUT))
        assert resp.status_code == 201, resp.text
        body = resp.json()
        tasks.created.append(body["id"])
        assert parse(body["due_date"]) == parse("2021-06-15T10:00:00Z")
        assert body["status"] == "pending"


class TestOverdueFiltering:
    def test_overdue_lists_only_active_past_due_tasks(self, tasks):
        p = tasks.create("pending", due_date="2020-03-01T00:00:00Z")
        ip = tasks.create("inprogress", due_date="2020-02-01T00:00:00Z")
        tasks.set_status(ip["id"], "in_progress")
        oh = tasks.create("onhold", due_date="2020-01-01T00:00:00Z")
        tasks.set_status(oh["id"], "on_hold")
        c = tasks.create("completed", due_date="2019-01-01T00:00:00Z")
        tasks.set_status(c["id"], "completed")
        x = tasks.create("cancelled", due_date="2019-01-01T00:00:00Z")
        tasks.set_status(x["id"], "cancelled")
        f = tasks.create("future", due_date=iso(datetime.now(timezone.utc) + timedelta(days=1)))
        n = tasks.create("nodue")

        resp = record(requests.get(OVERDUE, timeout=TIMEOUT))
        assert resp.status_code == 200
        body = resp.json()
        ids = [t["id"] for t in body["tasks"]]
        assert ids == [oh["id"], ip["id"], p["id"]]
        assert body["total_count"] == 3
        for excluded in (c, x, f, n):
            assert excluded["id"] not in ids
        statuses = {t["id"]: t["status"] for t in body["tasks"]}
        assert statuses == {oh["id"]: "on_hold", ip["id"]: "in_progress", p["id"]: "pending"}

    def test_overdue_task_removed_after_completion(self, tasks):
        t = tasks.create("tocomplete", due_date="2020-05-05T00:00:00Z")
        before = requests.get(OVERDUE, timeout=TIMEOUT).json()
        assert t["id"] in [x["id"] for x in before["tasks"]]
        tasks.set_status(t["id"], "completed")
        resp = record(requests.get(OVERDUE, timeout=TIMEOUT))
        assert resp.status_code == 200
        assert t["id"] not in [x["id"] for x in resp.json()["tasks"]]

    def test_overdue_recently_past_due_included(self, tasks):
        # Due a few seconds ago: strictly before now -> overdue
        t = tasks.create("justpast", due_date=iso(datetime.now(timezone.utc) - timedelta(seconds=5)))
        resp = record(requests.get(OVERDUE, timeout=TIMEOUT))
        assert resp.status_code == 200
        assert [x["id"] for x in resp.json()["tasks"]] == [t["id"]]


class TestOverdueOrdering:
    def test_overdue_sorted_by_due_date_then_id(self, tasks):
        same1 = tasks.create("same1", due_date="2020-06-01T00:00:00Z")
        same2 = tasks.create("same2", due_date="2020-06-01T00:00:00Z")
        same3 = tasks.create("same3", due_date="2020-06-01T00:00:00Z")
        early = tasks.create("early", due_date="2020-01-15T00:00:00Z")
        late = tasks.create("late", due_date="2021-01-15T00:00:00Z")

        resp = record(requests.get(OVERDUE, timeout=TIMEOUT))
        assert resp.status_code == 200
        ids = [t["id"] for t in resp.json()["tasks"]]
        tied = sorted([same1["id"], same2["id"], same3["id"]])
        assert ids == [early["id"]] + tied + [late["id"]]


class TestOverduePagination:
    def _seed_five(self, tasks):
        return [tasks.create("pg%d" % i, due_date="2020-01-0%dT00:00:00Z" % (i + 1)) for i in range(5)]

    def test_overdue_pagination_first_page(self, tasks):
        seeded = self._seed_five(tasks)
        resp = record(requests.get(OVERDUE, params={"page_size": 2}, timeout=TIMEOUT))
        assert resp.status_code == 200
        body = resp.json()
        assert [t["id"] for t in body["tasks"]] == [seeded[0]["id"], seeded[1]["id"]]
        assert body["next_page_token"] == "2"
        assert body["total_count"] == 5

    def test_overdue_pagination_last_page(self, tasks):
        seeded = self._seed_five(tasks)
        resp = record(requests.get(OVERDUE, params={"page_size": 2, "page_token": "4"}, timeout=TIMEOUT))
        assert resp.status_code == 200
        body = resp.json()
        assert [t["id"] for t in body["tasks"]] == [seeded[4]["id"]]
        assert "next_page_token" not in body
        assert body["total_count"] == 5

    def test_overdue_pagination_walk_all_pages(self, tasks):
        seeded = self._seed_five(tasks)
        collected, token, pages = [], None, 0
        while True:
            params = {"page_size": 2}
            if token:
                params["page_token"] = token
            resp = requests.get(OVERDUE, params=params, timeout=TIMEOUT)
            assert resp.status_code == 200
            body = resp.json()
            collected += [t["id"] for t in body["tasks"]]
            pages += 1
            token = body.get("next_page_token")
            if not token:
                break
        record(resp)
        assert pages == 3
        assert collected == [t["id"] for t in seeded]

    def test_overdue_default_page_size_is_20(self, tasks):
        for i in range(21):
            tasks.create("d%02d" % i, due_date="2020-02-%02dT00:00:00Z" % (i + 1))
        resp = record(requests.get(OVERDUE, timeout=TIMEOUT))
        assert resp.status_code == 200
        body = resp.json()
        assert len(body["tasks"]) == 20
        assert body["next_page_token"] == "20"
        assert body["total_count"] == 21

    def test_overdue_page_size_100_accepted(self, tasks):
        tasks.create("max", due_date="2020-01-01T00:00:00Z")
        resp = record(requests.get(OVERDUE, params={"page_size": 100}, timeout=TIMEOUT))
        assert resp.status_code == 200
        assert resp.json()["total_count"] == 1

    def test_overdue_page_size_1_accepted(self, tasks):
        tasks.create("a", due_date="2020-01-01T00:00:00Z")
        tasks.create("b", due_date="2020-01-02T00:00:00Z")
        resp = record(requests.get(OVERDUE, params={"page_size": 1}, timeout=TIMEOUT))
        assert resp.status_code == 200
        body = resp.json()
        assert len(body["tasks"]) == 1
        assert body["next_page_token"] == "1"


class TestOverdueValidation:
    def _assert_validation_error(self, resp):
        assert resp.status_code == 400
        assert resp.json()["error"]["code"] == "VALIDATION_ERROR"

    def test_overdue_page_size_zero_rejected(self):
        self._assert_validation_error(record(requests.get(OVERDUE, params={"page_size": 0}, timeout=TIMEOUT)))

    def test_overdue_page_size_101_rejected(self):
        self._assert_validation_error(record(requests.get(OVERDUE, params={"page_size": 101}, timeout=TIMEOUT)))

    def test_overdue_page_size_negative_rejected(self):
        self._assert_validation_error(record(requests.get(OVERDUE, params={"page_size": -5}, timeout=TIMEOUT)))

    def test_overdue_page_size_non_numeric_rejected(self):
        self._assert_validation_error(record(requests.get(OVERDUE, params={"page_size": "abc"}, timeout=TIMEOUT)))


class TestOverdueRouting:
    def test_overdue_not_routed_as_task_id(self):
        resp = record(requests.get(OVERDUE, timeout=TIMEOUT))
        assert resp.status_code == 200
        body = resp.json()
        assert "error" not in body
        assert "tasks" in body and "total_count" in body
        assert "id" not in body


class TestOverdueNegativePageToken:
    def test_overdue_negative_page_token_returns_first_page(self, tasks):
        """A negative page_token is ignored (documented, intentional): first page, no 500."""
        now = datetime.now(timezone.utc)
        for i in range(3):
            due = (now - timedelta(days=10 - i)).strftime("%Y-%m-%dT%H:%M:%SZ")
            tasks.create("negtok_%d" % i, due_date=due)
        first = requests.get(OVERDUE, params={"page_size": 2}, timeout=TIMEOUT).json()
        resp = record(requests.get(OVERDUE, params={"page_size": 2, "page_token": "-1"}, timeout=TIMEOUT))
        assert resp.status_code == 200, resp.text
        body = resp.json()
        assert body["total_count"] == 3
        assert [t["id"] for t in body["tasks"]] == [t["id"] for t in first["tasks"]]
        assert body["next_page_token"] == "2"
