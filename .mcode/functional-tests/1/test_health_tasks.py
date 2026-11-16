import hashlib, json, os, requests, pytest

BASE = "http://localhost:" + os.environ.get("PORT", "8080")
T = 10


def _secret():
    s = os.environ.get("TASK_API_ADMIN_SECRET")
    if not s:
        p = os.path.join(os.environ.get("MCODE_DIR", ""), "lifecycle/target/env_vars.json")
        try:
            s = json.load(open(p))["secrets"].get("TASK_API_ADMIN_SECRET")
        except Exception:
            s = None
    return s


def health():
    return requests.get(BASE + "/health", timeout=T)


@pytest.fixture
def created():
    ids = []
    yield ids
    for i in ids:
        requests.delete(f"{BASE}/api/v1/tasks/{i}", timeout=T)


class TestHealth:
    def test_health_shape(self):
        r = health()
        assert r.status_code == 200
        b = r.json()
        for k in ("status", "timestamp", "service", "version", "process", "checks"):
            assert k in b
        ts = b["checks"]["task_service"]
        assert ts["status"] == "ok"
        assert isinstance(ts["task_count"], int) and ts["task_count"] >= 0

    def test_no_secret_leak(self):
        s = _secret()
        assert s
        body = health().text
        assert s not in body
        assert hashlib.sha256(s.encode()).hexdigest() not in body
        assert "secret" not in body.lower()


class TestTasks:
    def test_create_increases_count_and_delete_decreases(self, created):
        before = health().json()["checks"]["task_service"]["task_count"]
        r = requests.post(BASE + "/api/v1/tasks", json={"title": "ftrun_task"}, timeout=T)
        assert r.status_code == 201
        tid = r.json()["id"]
        created.append(tid)
        assert health().json()["checks"]["task_service"]["task_count"] == before + 1
        assert requests.delete(f"{BASE}/api/v1/tasks/{tid}", timeout=T).status_code == 204
        created.clear()
        assert health().json()["checks"]["task_service"]["task_count"] == before

    def test_list_and_stats(self, created):
        r = requests.post(BASE + "/api/v1/tasks", json={"title": "ftrun_list"}, timeout=T)
        created.append(r.json()["id"])
        l = requests.get(BASE + "/api/v1/tasks", timeout=T)
        assert l.status_code == 200
        s = requests.get(BASE + "/api/v1/tasks/stats", timeout=T)
        assert s.status_code == 200
        assert s.json()["total"] == health().json()["checks"]["task_service"]["task_count"]

    def test_create_invalid_missing_title(self):
        r = requests.post(BASE + "/api/v1/tasks", json={}, timeout=T)
        assert r.status_code == 400

    def test_get_not_found(self):
        assert requests.get(BASE + "/api/v1/tasks/ftrun_nonexistent", timeout=T).status_code == 404
