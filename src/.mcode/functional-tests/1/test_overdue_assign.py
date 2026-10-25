import os, subprocess, time, uuid, datetime as dt
import pytest, requests

SRC = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
PORT = "18080"
BASE = f"http://127.0.0.1:{PORT}"
API = BASE + "/api/v1/tasks"


def _start(fail_notifier):
    env = dict(os.environ, PORT=PORT, FAIL_NOTIFIER="1" if fail_notifier else "0", FT_HARNESS_TEST_ONLY="1")
    p = subprocess.Popen(["/tmp/ft_harness"], env=env, cwd=SRC,
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    for _ in range(50):
        try:
            if requests.get(BASE + "/health", timeout=1).status_code == 200:
                return p
        except requests.RequestException:
            time.sleep(0.1)
    p.kill()
    raise RuntimeError("harness did not start")


@pytest.fixture(scope="module")
def srv():
    p = _start(False)
    yield
    p.terminate(); p.wait()


def iso(delta_days):
    t = dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=delta_days)
    return t.strftime("%Y-%m-%dT%H:%M:%SZ")


def mk(title, due=None, status=None):
    body = {"title": title}
    if due:
        body["due_date"] = due
    r = requests.post(API, json=body, timeout=5)
    assert r.status_code == 201, r.text
    t = r.json()
    if status:
        r = requests.patch(f"{API}/{t['id']}/status", json={"status": status}, timeout=5)
        assert r.status_code == 200, r.text
    return t


def overdue_all(**params):
    out, tok = [], ""
    while True:
        q = dict(params, page_size=100)
        if tok:
            q["page_token"] = tok
        r = requests.get(API + "/overdue", params=q, timeout=5)
        assert r.status_code == 200, r.text
        j = r.json()
        out += j["tasks"]
        tok = j.get("next_page_token", "")
        if not tok:
            return out


class TestOverdue:
    def test_overdue_filter_and_order(self, srv):
        ns = "ftrun_" + uuid.uuid4().hex[:6]
        a = mk(f"{ns}-old", iso(-10))
        b = mk(f"{ns}-recent", iso(-1))
        c = mk(f"{ns}-future", iso(5))
        d = mk(f"{ns}-nodue")
        e = mk(f"{ns}-done", iso(-3), "completed")
        f = mk(f"{ns}-cancel", iso(-3), "cancelled")
        g = mk(f"{ns}-inprog", iso(-2), "in_progress")
        r = requests.get(API + "/overdue", timeout=5)
        assert r.status_code == 200
        assert "tasks" in r.json() and "total_count" in r.json()
        tasks = overdue_all()
        ids = [t["id"] for t in tasks]
        for t in (a, b, g):
            assert t["id"] in ids
        for t in (c, d, e, f):
            assert t["id"] not in ids
        dues = [t["due_date"] for t in tasks]
        assert dues == sorted(dues)
        for t in tasks:
            assert t["status"] not in ("completed", "cancelled")
            assert t["due_date"] is not None
        assert ids.index(a["id"]) < ids.index(g["id"]) < ids.index(b["id"])

    def test_overdue_pagination_and_tiebreak(self, srv):
        due = iso(-20)
        made = [mk("ftrun_tie", due) for _ in range(5)]
        full = overdue_all()
        # walk with page_size=2
        seen, tok, pages = [], "", 0
        while True:
            q = {"page_size": 2}
            if tok:
                q["page_token"] = tok
            j = requests.get(API + "/overdue", params=q, timeout=5).json()
            assert len(j["tasks"]) <= 2
            seen += [t["id"] for t in j["tasks"]]
            tok = j.get("next_page_token", "")
            pages += 1
            if not tok:
                break
        assert pages >= 3
        assert seen == [t["id"] for t in full]
        assert len(set(seen)) == len(seen)
        tied = [t["id"] for t in full if t["due_date"] == due]
        assert tied == sorted(tied) and len(tied) >= 5

    def test_overdue_not_shadowed_by_id(self, srv):
        r = requests.get(API + "/overdue", timeout=5)
        assert r.status_code == 200 and "tasks" in r.json()

    @pytest.mark.parametrize("ps", ["0", "101", "abc"])
    def test_overdue_invalid_page_size(self, srv, ps):
        r = requests.get(API + "/overdue", params={"page_size": ps}, timeout=5)
        assert r.status_code == 400
        assert r.json()["error"]["code"] == "VALIDATION_ERROR"


class TestAssign:
    def test_assign_ok(self, srv):
        t = mk("ftrun_assign")
        r = requests.post(f"{API}/{t['id']}/assign", json={"assigned_to": "bob@example.com"}, timeout=5)
        assert r.status_code == 200
        assert r.json()["assigned_to"] == "bob@example.com"
        g = requests.get(f"{API}/{t['id']}", timeout=5).json()
        assert g["assigned_to"] == "bob@example.com"

    @pytest.mark.parametrize("bad", ["notanemail", "Bob <bob@example.com>", "a@", "@b.com", "", "  "])
    def test_assign_invalid_email(self, srv, bad):
        t = mk("ftrun_assign_bad")
        r = requests.post(f"{API}/{t['id']}/assign", json={"assigned_to": bad}, timeout=5)
        assert r.status_code == 400
        assert r.json()["error"]["code"] == "VALIDATION_ERROR"
        assert "assigned_to" not in requests.get(f"{API}/{t['id']}", timeout=5).json()

    def test_assign_missing_field(self, srv):
        t = mk("ftrun_assign_missing")
        r = requests.post(f"{API}/{t['id']}/assign", json={}, timeout=5)
        assert r.status_code == 400
        assert r.json()["error"]["code"] == "VALIDATION_ERROR"

    def test_assign_unknown_task(self, srv):
        r = requests.post(f"{API}/does-not-exist/assign", json={"assigned_to": "a@b.com"}, timeout=5)
        assert r.status_code == 404
        assert r.json()["error"]["code"] == "NOT_FOUND"

    def test_assign_notifier_failure_still_200(self):
        p = _start(True)
        try:
            t = mk("ftrun_notifyfail")
            r = requests.post(f"{API}/{t['id']}/assign", json={"assigned_to": "x@example.com"}, timeout=5)
            assert r.status_code == 200
            assert r.json()["assigned_to"] == "x@example.com"
        finally:
            p.terminate(); p.wait()


class TestListPaginationRegression:
    def test_list_pagination_unchanged(self, srv):
        for i in range(5):
            mk(f"ftrun_list_{i}")
        full = requests.get(API, params={"page_size": 100, "sort_order": "created_at_asc"}, timeout=5).json()
        total = full["total_count"]
        assert total == len(full["tasks"])
        seen, tok = [], ""
        while True:
            q = {"page_size": 2, "sort_order": "created_at_asc"}
            if tok:
                q["page_token"] = tok
            j = requests.get(API, params=q, timeout=5).json()
            assert j["total_count"] == total
            seen += [t["id"] for t in j["tasks"]]
            tok = j.get("next_page_token", "")
            if not tok:
                break
            assert tok == str(len(seen))
        # default list order is map-iteration based (pre-existing, unspecified), so use created_at_asc; compare as sets
        assert sorted(seen) == sorted(t["id"] for t in full["tasks"])
        assert len(set(seen)) == len(seen)

    def test_list_default_and_invalid(self, srv):
        r = requests.get(API, timeout=5)
        assert r.status_code == 200
        assert len(r.json()["tasks"]) <= 20
        assert requests.get(API, params={"page_size": 101}, timeout=5).status_code == 400
        j = requests.get(API, params={"page_token": "99999"}, timeout=5).json()
        assert not j.get("tasks") and not j.get("next_page_token")


class TestTaskNotFoundMapping:
    MISSING = "ftrun-missing-task-id"

    def _assert_404(self, r):
        assert r.status_code == 404, r.text
        err = r.json()["error"]
        assert err["code"] == "NOT_FOUND"
        assert err["message"] == f"task with ID {self.MISSING} not found"

    def test_get_missing(self, srv):
        self._assert_404(requests.get(f"{API}/{self.MISSING}", timeout=5))

    def test_put_missing(self, srv):
        self._assert_404(requests.put(f"{API}/{self.MISSING}", json={"title": "x"}, timeout=5))

    def test_patch_status_missing(self, srv):
        self._assert_404(requests.patch(f"{API}/{self.MISSING}/status", json={"status": "completed"}, timeout=5))

    def test_delete_missing(self, srv):
        self._assert_404(requests.delete(f"{API}/{self.MISSING}", timeout=5))

    def test_list_comments_missing_task(self, srv):
        self._assert_404(requests.get(f"{API}/{self.MISSING}/comments", timeout=5))

    def test_add_comment_missing_task(self, srv):
        self._assert_404(requests.post(f"{API}/{self.MISSING}/comments",
                                       json={"author": "a@b.com", "body": "hi"}, timeout=5))

    def test_put_existing_ok_and_invalid_status(self, srv):
        t = mk("ftrun_put")
        r = requests.put(f"{API}/{t['id']}", json={"title": "ftrun_put_renamed"}, timeout=5)
        assert r.status_code == 200 and r.json()["title"] == "ftrun_put_renamed"
        r = requests.patch(f"{API}/{t['id']}/status", json={"status": "in_progress"}, timeout=5)
        assert r.status_code == 200 and r.json()["status"] == "in_progress"
        r = requests.patch(f"{API}/{t['id']}/status", json={"status": "bogus"}, timeout=5)
        assert r.status_code == 400


class TestDefaultPageSize:
    def test_default_page_size_20_list_and_overdue(self, srv):
        for i in range(25):
            mk(f"ftrun_dflt_{i}", iso(-30))
        r = requests.get(API, timeout=5).json()
        assert len(r["tasks"]) == 20 and r.get("next_page_token")
        r = requests.get(API + "/overdue", timeout=5).json()
        assert len(r["tasks"]) == 20 and r.get("next_page_token")

    def test_negative_page_token_is_first_page(self, srv):
        a = requests.get(API + "/overdue", params={"page_size": 3}, timeout=5)
        b = requests.get(API + "/overdue", params={"page_size": 3, "page_token": "-5"}, timeout=5)
        assert a.status_code == 200 and b.status_code == 200
        assert [t["id"] for t in a.json()["tasks"]] == [t["id"] for t in b.json()["tasks"]]
