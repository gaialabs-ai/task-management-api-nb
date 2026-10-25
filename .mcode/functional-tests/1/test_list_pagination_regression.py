"""Regression: GET /api/v1/tasks pagination/filter/sort behavior unchanged after helper extraction."""
import pytest
import requests

from helpers import API, TIMEOUT, assert_error, create_task, days_ago, new_run_id, set_status, walk_pages


@pytest.fixture(scope="module")
def bulk():
    run = new_run_id()
    created = []
    for i in range(25):
        created.append(create_task("%s item %02d" % (run, i), tags=[run, "even" if i % 2 == 0 else "odd"],
                                   priority=["low", "medium", "high", "critical"][i % 4]))
    return {"run": run, "tasks": created}


class TestListPagination:
    def test_default_page_size_20_and_next_token(self, bulk):
        resp = requests.get(API + "/tasks", params={"tags": bulk["run"]}, timeout=TIMEOUT)
        assert resp.status_code == 200
        body = resp.json()
        assert len(body["tasks"]) == 20
        assert body["total_count"] == 25
        assert body["next_page_token"] == "20"

    def test_second_page_via_token_is_last(self, bulk):
        resp = requests.get(API + "/tasks", params={"tags": bulk["run"], "page_token": "20"}, timeout=TIMEOUT)
        body = resp.json()
        assert resp.status_code == 200
        assert len(body["tasks"]) == 5
        assert body["total_count"] == 25
        assert not body.get("next_page_token")

    def test_walk_pages_of_7_returns_all_unique(self, bulk):
        tasks, pages, _ = walk_pages("/tasks", 7, {"tags": bulk["run"], "sort_order": "created_at_asc"})
        assert len(pages) == 4
        assert [len(p["tasks"]) for p in pages] == [7, 7, 7, 4]
        assert [t["id"] for t in tasks] == [t["id"] for t in bulk["tasks"]]

    def test_default_order_is_stable_across_pages(self, bulk):
        """Without sort_order, paging must still cover every task exactly once.

        KNOWN PRE-EXISTING BUG (present in baseline c0282c3, not introduced by the refactor):
        ListTasks ranges over a Go map and sortTasks is a no-op for an empty sort_order, so
        the order differs between requests and page_token offsets skip/duplicate tasks.
        """
        tasks, _, _ = walk_pages("/tasks", 7, {"tags": bulk["run"]})
        ids = [t["id"] for t in tasks]
        assert len(ids) == len(set(ids)), "duplicate tasks across pages with default ordering"
        assert sorted(ids) == sorted(t["id"] for t in bulk["tasks"]), "tasks skipped across pages"

    def test_exact_multiple_has_no_trailing_token(self, bulk):
        resp = requests.get(API + "/tasks", params={"tags": bulk["run"], "page_size": 25}, timeout=TIMEOUT)
        body = resp.json()
        assert len(body["tasks"]) == 25
        assert not body.get("next_page_token")

    def test_page_size_100_accepted(self, bulk):
        resp = requests.get(API + "/tasks", params={"tags": bulk["run"], "page_size": 100}, timeout=TIMEOUT)
        assert resp.status_code == 200 and len(resp.json()["tasks"]) == 25

    @pytest.mark.parametrize("value", ["0", "101", "-5", "abc"])
    def test_invalid_page_size_400(self, value):
        assert_error(requests.get(API + "/tasks", params={"page_size": value}, timeout=TIMEOUT),
                     400, "VALIDATION_ERROR")

    def test_token_past_end_returns_no_tasks_but_total(self, bulk):
        resp = requests.get(API + "/tasks", params={"tags": bulk["run"], "page_token": "500"}, timeout=TIMEOUT)
        body = resp.json()
        assert resp.status_code == 200
        assert not body["tasks"]
        assert body["total_count"] == 25
        assert not body.get("next_page_token")

    def test_non_numeric_token_starts_at_beginning(self, bulk):
        resp = requests.get(API + "/tasks", params={"tags": bulk["run"], "page_size": 3, "page_token": "zzz"},
                            timeout=TIMEOUT)
        assert resp.status_code == 200
        assert len(resp.json()["tasks"]) == 3
        assert resp.json()["next_page_token"] == "3"

    def test_negative_token_does_not_crash_server(self, bulk):
        resp = requests.get(API + "/tasks", params={"tags": bulk["run"], "page_token": "-3"}, timeout=TIMEOUT)
        assert resp.status_code == 200
        # server must still be healthy afterwards
        assert requests.get(API.replace("/api/v1", "") + "/health", timeout=TIMEOUT).status_code == 200


class TestListFilters:
    def test_status_filter(self, bulk):
        run = new_run_id()
        a = create_task("%s a" % run, tags=[run])
        b = create_task("%s b" % run, tags=[run])
        set_status(b["id"], "in_progress")
        resp = requests.get(API + "/tasks", params={"tags": run, "status": "in_progress"}, timeout=TIMEOUT)
        body = resp.json()
        assert [t["id"] for t in body["tasks"]] == [b["id"]]
        assert body["total_count"] == 1
        resp = requests.get(API + "/tasks", params={"tags": run, "status": "pending"}, timeout=TIMEOUT)
        assert [t["id"] for t in resp.json()["tasks"]] == [a["id"]]

    def test_assigned_to_filter(self):
        run = new_run_id()
        email = "%s@example.com" % run
        t = create_task("%s assigned" % run, assigned_to=email)
        create_task("%s other" % run)
        body = requests.get(API + "/tasks", params={"assigned_to": email}, timeout=TIMEOUT).json()
        assert [x["id"] for x in body["tasks"]] == [t["id"]]

    def test_tags_filter_requires_all_tags(self, bulk):
        body = requests.get(API + "/tasks", params={"tags": bulk["run"] + ",even", "page_size": 100},
                            timeout=TIMEOUT).json()
        assert body["total_count"] == 13
        assert all("even" in t["tags"] for t in body["tasks"])

    def test_combined_filters_and_pagination(self, bulk):
        tasks, pages, _ = walk_pages("/tasks", 5, {"tags": bulk["run"] + ",odd", "sort_order": "created_at_asc"})
        assert len(tasks) == 12
        assert len(set(t["id"] for t in tasks)) == 12
        assert pages[0]["total_count"] == 12

    def test_sort_created_at_desc_and_asc(self, bulk):
        asc = requests.get(API + "/tasks", params={"tags": bulk["run"], "page_size": 100,
                                                   "sort_order": "created_at_asc"}, timeout=TIMEOUT).json()["tasks"]
        desc = requests.get(API + "/tasks", params={"tags": bulk["run"], "page_size": 100,
                                                    "sort_order": "created_at_desc"}, timeout=TIMEOUT).json()["tasks"]
        assert [t["id"] for t in asc] == [t["id"] for t in bulk["tasks"]]
        assert [t["id"] for t in desc] == [t["id"] for t in reversed(bulk["tasks"])]

    def test_sort_priority_desc(self, bulk):
        rank = {"low": 1, "medium": 2, "high": 3, "critical": 4}
        tasks = requests.get(API + "/tasks", params={"tags": bulk["run"], "page_size": 100,
                                                     "sort_order": "priority_desc"}, timeout=TIMEOUT).json()["tasks"]
        ranks = [rank[t["priority"]] for t in tasks]
        assert ranks == sorted(ranks, reverse=True)

    def test_sort_due_date_asc_pagination_stable(self):
        run = new_run_id()
        ids = []
        for n in (5, 3, 9, 1, 7):
            ids.append((n, create_task("%s due%d" % (run, n), tags=[run], due_date=days_ago(n))["id"]))
        tasks, _, _ = walk_pages("/tasks", 2, {"tags": run, "sort_order": "due_date_asc"})
        expected = [i for _, i in sorted(ids, key=lambda p: -p[0])]  # oldest (largest n) first
        assert [t["id"] for t in tasks] == expected

    def test_sample_data_present_in_unfiltered_list(self):
        body = requests.get(API + "/tasks", params={"page_size": 100}, timeout=TIMEOUT).json()
        assert body["total_count"] >= 2
