"""GET /api/v1/tasks/overdue - filtering, ordering, pagination, route precedence."""
import pytest
import requests

from helpers import (API, TIMEOUT, assert_error, create_task, days_ago, days_ahead, get_task,
                     new_run_id, set_status, walk_pages)


@pytest.fixture(scope="module")
def seeded():
    """Create a namespaced set of tasks covering every overdue edge case."""
    run = new_run_id()
    t = {}
    # Created in this order; due dates are far in the past so they sort before any other test data.
    t["old_pending"] = create_task("%s old_pending" % run, due_date="2000-01-01T00:00:00Z", tags=[run])
    t["tie_later_created"] = create_task("%s tie_later" % run, due_date="2000-01-01T00:00:00Z", tags=[run])
    t["mid_inprogress"] = create_task("%s mid_inprogress" % run, due_date="2000-01-03T00:00:00Z", tags=[run])
    set_status(t["mid_inprogress"]["id"], "in_progress")
    t["onhold"] = create_task("%s onhold" % run, due_date="2000-01-04T00:00:00Z", tags=[run])
    set_status(t["onhold"]["id"], "on_hold")
    t["completed"] = create_task("%s completed" % run, due_date="2000-01-05T00:00:00Z", tags=[run])
    set_status(t["completed"]["id"], "completed")
    t["cancelled"] = create_task("%s cancelled" % run, due_date="2000-01-06T00:00:00Z", tags=[run])
    set_status(t["cancelled"]["id"], "cancelled")
    t["future"] = create_task("%s future" % run, due_date=days_ahead(30), tags=[run])
    t["nodue"] = create_task("%s nodue" % run, tags=[run])
    t["just_past"] = create_task("%s just_past" % run, due_date=days_ago(1), tags=[run])
    t["just_future"] = create_task("%s just_future" % run, due_date=days_ahead(1), tags=[run])
    t["run"] = run
    return t


def my_ids(tasks, run):
    return [x["id"] for x in tasks if run in (x.get("tags") or [])]


class TestOverdueListing:
    def test_returns_200_with_response_shape(self, seeded):
        resp = requests.get(API + "/tasks/overdue", timeout=TIMEOUT)
        assert resp.status_code == 200
        body = resp.json()
        assert set(["tasks", "total_count"]).issubset(body.keys())
        assert isinstance(body["tasks"], list)
        assert isinstance(body["total_count"], int) and body["total_count"] >= 6
        assert len(body["tasks"]) <= 20  # default page size
        assert "application/json" in resp.headers["Content-Type"]

    def test_overdue_included_and_excluded_correctly(self, seeded):
        tasks, _, _ = walk_pages("/tasks/overdue", 100)
        ids = set(x["id"] for x in tasks)
        for key in ("old_pending", "tie_later_created", "mid_inprogress", "onhold", "just_past"):
            assert seeded[key]["id"] in ids, "%s should be overdue" % key
        for key in ("completed", "cancelled", "future", "nodue", "just_future"):
            assert seeded[key]["id"] not in ids, "%s must not be overdue" % key
        # Every returned task satisfies the overdue predicate.
        for x in tasks:
            assert x["due_date"] is not None
            assert x["status"] not in ("completed", "cancelled")

    def test_sorted_by_due_date_ascending_with_created_at_tiebreak(self, seeded):
        tasks, _, _ = walk_pages("/tasks/overdue", 100)
        due = [x["due_date"] for x in tasks]
        assert due == sorted(due), "due dates must be ascending"
        mine = my_ids(tasks, seeded["run"])
        expected = [seeded[k]["id"] for k in
                    ("old_pending", "tie_later_created", "mid_inprogress", "onhold", "just_past")]
        assert mine == expected

    def test_task_transitions_into_and_out_of_overdue(self):
        run = new_run_id()
        task = create_task("%s transition" % run, due_date="2001-02-03T04:05:06Z", tags=[run])
        tasks, _, _ = walk_pages("/tasks/overdue", 100)
        assert task["id"] in [x["id"] for x in tasks]
        set_status(task["id"], "completed")
        tasks, _, _ = walk_pages("/tasks/overdue", 100)
        assert task["id"] not in [x["id"] for x in tasks]

    def test_updating_due_date_to_future_removes_from_overdue(self):
        run = new_run_id()
        task = create_task("%s due_update" % run, due_date="2001-02-03T04:05:06Z", tags=[run])
        resp = requests.put(API + "/tasks/%s" % task["id"], json={"due_date": days_ahead(10)}, timeout=TIMEOUT)
        assert resp.status_code == 200
        tasks, _, _ = walk_pages("/tasks/overdue", 100)
        assert task["id"] not in [x["id"] for x in tasks]

    def test_deleted_task_not_listed(self):
        task = create_task("%s deleted" % new_run_id(), due_date="2001-02-03T04:05:06Z")
        assert requests.delete(API + "/tasks/%s" % task["id"], timeout=TIMEOUT).status_code == 204
        tasks, _, _ = walk_pages("/tasks/overdue", 100)
        assert task["id"] not in [x["id"] for x in tasks]

    def test_assigned_overdue_task_reports_assignee(self):
        run = new_run_id()
        task = create_task("%s assigned_overdue" % run, due_date="2001-02-03T04:05:06Z", tags=[run])
        r = requests.post(API + "/tasks/%s/assign" % task["id"], json={"assigned_to": "o@example.com"},
                          timeout=TIMEOUT)
        assert r.status_code == 200
        tasks, _, _ = walk_pages("/tasks/overdue", 100)
        match = [x for x in tasks if x["id"] == task["id"]]
        assert match and match[0]["assigned_to"] == "o@example.com"


class TestOverduePagination:
    def test_walk_with_small_page_size_covers_all_without_duplicates(self, seeded):
        all_small, pages, last = walk_pages("/tasks/overdue", 2)
        all_big, _, _ = walk_pages("/tasks/overdue", 100)
        assert [x["id"] for x in all_small] == [x["id"] for x in all_big]
        assert len(set(x["id"] for x in all_small)) == len(all_small)
        totals = set(p["total_count"] for p in pages)
        assert len(totals) == 1 and totals.pop() == len(all_small)
        for p in pages[:-1]:
            assert len(p["tasks"]) == 2
            assert p["next_page_token"]
        assert not last.get("next_page_token")

    def test_page_size_one_first_page_and_token_is_offset(self, seeded):
        resp = requests.get(API + "/tasks/overdue", params={"page_size": 1}, timeout=TIMEOUT)
        body = resp.json()
        assert resp.status_code == 200
        assert len(body["tasks"]) == 1
        assert body["next_page_token"] == "1"
        nxt = requests.get(API + "/tasks/overdue", params={"page_size": 1, "page_token": body["next_page_token"]},
                           timeout=TIMEOUT).json()
        assert nxt["tasks"][0]["id"] != body["tasks"][0]["id"]
        assert nxt["tasks"][0]["due_date"] >= body["tasks"][0]["due_date"]

    def test_page_size_max_100_accepted(self, seeded):
        resp = requests.get(API + "/tasks/overdue", params={"page_size": 100}, timeout=TIMEOUT)
        assert resp.status_code == 200
        assert len(resp.json()["tasks"]) <= 100

    def test_page_token_past_end_returns_empty_non_nil_array(self, seeded):
        resp = requests.get(API + "/tasks/overdue", params={"page_token": "999999"}, timeout=TIMEOUT)
        assert resp.status_code == 200
        body = resp.json()
        assert body["tasks"] == []
        assert not body.get("next_page_token")
        assert body["total_count"] >= 6

    def test_default_page_size_is_20(self):
        run = new_run_id()
        for i in range(22):
            create_task("%s bulk %02d" % (run, i), due_date="1999-12-%02dT00:00:00Z" % (1 + (i % 28)), tags=[run])
        resp = requests.get(API + "/tasks/overdue", timeout=TIMEOUT)
        body = resp.json()
        assert len(body["tasks"]) == 20
        assert body["next_page_token"] == "20"
        assert body["total_count"] >= 22

    @pytest.mark.parametrize("value", ["0", "101", "1000", "-1", "abc"])
    def test_invalid_page_size_rejected(self, value):
        resp = requests.get(API + "/tasks/overdue", params={"page_size": value}, timeout=TIMEOUT)
        assert_error(resp, 400, "VALIDATION_ERROR")


class TestRoutePrecedence:
    def test_overdue_not_treated_as_task_id(self):
        resp = requests.get(API + "/tasks/overdue", timeout=TIMEOUT)
        assert resp.status_code == 200
        body = resp.json()
        assert "tasks" in body and "error" not in body
        # whereas an arbitrary id still goes to /:id and 404s
        assert_error(requests.get(API + "/tasks/overdue-not-a-task", timeout=TIMEOUT), 404, "NOT_FOUND")

    def test_get_task_by_id_still_works(self):
        task = create_task("%s byid" % new_run_id())
        resp = get_task(task["id"])
        assert resp.status_code == 200 and resp.json()["id"] == task["id"]

    def test_stats_and_search_routes_unaffected(self):
        assert requests.get(API + "/tasks/stats", timeout=TIMEOUT).status_code == 200
        assert requests.get(API + "/tasks/search", params={"q": "go"}, timeout=TIMEOUT).status_code == 200

    def test_trailing_overdue_does_not_accept_post(self):
        resp = requests.post(API + "/tasks/overdue", json={"title": "x"}, timeout=TIMEOUT)
        assert resp.status_code in (404, 405)
