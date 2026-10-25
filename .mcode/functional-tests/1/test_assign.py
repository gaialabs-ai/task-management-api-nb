"""POST /api/v1/tasks/:id/assign - success, validation, not found."""
import time
from datetime import datetime

import pytest
import requests

from helpers import API, TIMEOUT, assert_error, create_task, get_task, new_run_id


def parse(ts):
    # Go RFC3339Nano: trim fractional seconds to microseconds for fromisoformat
    ts = ts.replace("Z", "+00:00")
    if "." in ts:
        head, rest = ts.split(".", 1)
        frac = ""
        i = 0
        while i < len(rest) and rest[i].isdigit():
            frac += rest[i]
            i += 1
        ts = head + "." + frac[:6].ljust(6, "0") + rest[i:]
    return datetime.fromisoformat(ts)


def assign(task_id, payload):
    return requests.post(API + "/tasks/%s/assign" % task_id, json=payload, timeout=TIMEOUT)


@pytest.fixture
def task():
    return create_task("%s assign target" % new_run_id())


class TestAssignSuccess:
    def test_assign_returns_updated_task(self, task):
        time.sleep(0.01)
        resp = assign(task["id"], {"assigned_to": "alice@example.com"})
        assert resp.status_code == 200
        body = resp.json()
        assert body["id"] == task["id"]
        assert body["assigned_to"] == "alice@example.com"
        assert body["title"] == task["title"]
        assert body["status"] == task["status"]
        assert parse(body["updated_at"]) > parse(task["updated_at"])
        assert body["created_at"] == task["created_at"]

    def test_get_reflects_assignment(self, task):
        assert assign(task["id"], {"assigned_to": "bob@example.com"}).status_code == 200
        got = get_task(task["id"])
        assert got.status_code == 200
        assert got.json()["assigned_to"] == "bob@example.com"

    def test_assignment_visible_via_list_filter(self, task):
        email = "filter_%s@example.com" % new_run_id()
        assert assign(task["id"], {"assigned_to": email}).status_code == 200
        resp = requests.get(API + "/tasks", params={"assigned_to": email}, timeout=TIMEOUT)
        assert resp.status_code == 200
        body = resp.json()
        assert body["total_count"] == 1
        assert body["tasks"][0]["id"] == task["id"]

    def test_reassign_overwrites(self, task):
        assert assign(task["id"], {"assigned_to": "first@example.com"}).status_code == 200
        resp = assign(task["id"], {"assigned_to": "second@example.com"})
        assert resp.status_code == 200
        assert resp.json()["assigned_to"] == "second@example.com"
        assert get_task(task["id"]).json()["assigned_to"] == "second@example.com"

    def test_whitespace_is_trimmed(self, task):
        resp = assign(task["id"], {"assigned_to": "   carol@example.com \t"})
        assert resp.status_code == 200
        assert resp.json()["assigned_to"] == "carol@example.com"
        assert get_task(task["id"]).json()["assigned_to"] == "carol@example.com"

    def test_plus_addressing_and_subdomain_accepted(self, task):
        resp = assign(task["id"], {"assigned_to": "dev.user+tag@mail.sub.example.co.uk"})
        assert resp.status_code == 200
        assert resp.json()["assigned_to"] == "dev.user+tag@mail.sub.example.co.uk"

    def test_assign_task_with_existing_assignee_from_create(self):
        t = create_task("%s pre_assigned" % new_run_id(), assigned_to="old@example.com")
        resp = assign(t["id"], {"assigned_to": "new@example.com"})
        assert resp.status_code == 200 and resp.json()["assigned_to"] == "new@example.com"


class TestAssignValidation:
    @pytest.mark.parametrize("bad", [
        "Alice <alice@example.com>",
        '"Alice" <alice@example.com>',
        "<alice@example.com>",
        "not-an-email",
        "alice@",
        "@example.com",
        "alice example@example.com",
        "a@b.com, c@d.com",
    ])
    def test_invalid_email_rejected(self, task, bad):
        assert_error(assign(task["id"], {"assigned_to": bad}), 400, "VALIDATION_ERROR")
        assert get_task(task["id"]).json().get("assigned_to") is None

    def test_failed_assignment_does_not_change_existing_assignee(self, task):
        assert assign(task["id"], {"assigned_to": "keep@example.com"}).status_code == 200
        assert_error(assign(task["id"], {"assigned_to": "Bad <bad@example.com>"}), 400, "VALIDATION_ERROR")
        assert get_task(task["id"]).json()["assigned_to"] == "keep@example.com"

    def test_missing_field(self, task):
        assert_error(assign(task["id"], {}), 400, "VALIDATION_ERROR")

    def test_empty_string(self, task):
        assert_error(assign(task["id"], {"assigned_to": ""}), 400, "VALIDATION_ERROR")

    def test_whitespace_only(self, task):
        assert_error(assign(task["id"], {"assigned_to": "    "}), 400, "VALIDATION_ERROR")

    def test_wrong_type(self, task):
        assert_error(assign(task["id"], {"assigned_to": 123}), 400, "VALIDATION_ERROR")

    def test_null_value(self, task):
        assert_error(assign(task["id"], {"assigned_to": None}), 400, "VALIDATION_ERROR")

    def test_malformed_json(self, task):
        resp = requests.post(API + "/tasks/%s/assign" % task["id"], data="{not json",
                             headers={"Content-Type": "application/json"}, timeout=TIMEOUT)
        assert_error(resp, 400, "VALIDATION_ERROR")

    def test_empty_body(self, task):
        resp = requests.post(API + "/tasks/%s/assign" % task["id"], data="",
                             headers={"Content-Type": "application/json"}, timeout=TIMEOUT)
        assert_error(resp, 400, "VALIDATION_ERROR")


class TestAssignNotFound:
    def test_unknown_task(self):
        resp = assign("00000000-0000-0000-0000-000000000000", {"assigned_to": "x@example.com"})
        assert_error(resp, 404, "NOT_FOUND")

    def test_deleted_task(self):
        t = create_task("%s to_delete" % new_run_id())
        assert requests.delete(API + "/tasks/%s" % t["id"], timeout=TIMEOUT).status_code == 204
        assert_error(assign(t["id"], {"assigned_to": "x@example.com"}), 404, "NOT_FOUND")

    def test_get_on_assign_route_not_allowed(self, task):
        resp = requests.get(API + "/tasks/%s/assign" % task["id"], timeout=TIMEOUT)
        assert resp.status_code in (404, 405)
