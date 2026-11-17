import uuid, requests, pytest

BASE = "http://localhost:8080"
T = BASE + "/api/v1/tasks"
RUN = "ftrun_" + uuid.uuid4().hex[:6]


@pytest.fixture(scope="module")
def task():
    r = requests.post(T, json={"title": f"{RUN}_main", "description": "d", "priority": "high", "tags": [RUN]}, timeout=10)
    assert r.status_code == 201, r.text
    t = r.json()
    yield t
    requests.delete(f"{T}/{t['id']}", timeout=10)


def test_create_shape(task):
    assert task["title"] == f"{RUN}_main" and task["id"] and task["status"]


def test_get(task):
    r = requests.get(f"{T}/{task['id']}", timeout=10)
    assert r.status_code == 200 and r.json()["id"] == task["id"]


def test_list():
    r = requests.get(T, timeout=10)
    assert r.status_code == 200 and "tasks" in r.json()


def test_search(task):
    r = requests.get(T + "/search", params={"q": RUN}, timeout=10)
    assert r.status_code == 200
    assert any(x["id"] == task["id"] for x in r.json()["tasks"])


def test_search_missing_q():
    assert requests.get(T + "/search", timeout=10).status_code == 400


def test_stats():
    r = requests.get(T + "/stats", timeout=10)
    assert r.status_code == 200 and isinstance(r.json(), dict)


def test_update(task):
    r = requests.put(f"{T}/{task['id']}", json={"title": f"{RUN}_upd"}, timeout=10)
    assert r.status_code == 200 and r.json()["title"] == f"{RUN}_upd"


def test_patch_status(task):
    r = requests.patch(f"{T}/{task['id']}/status", json={"status": "in_progress"}, timeout=10)
    assert r.status_code == 200 and r.json()["status"] == "in_progress"


def test_patch_status_invalid(task):
    assert requests.patch(f"{T}/{task['id']}/status", json={}, timeout=10).status_code == 400


def test_create_invalid():
    assert requests.post(T, json={"description": "no title"}, timeout=10).status_code == 400


def test_get_404():
    assert requests.get(T + "/nope-" + RUN, timeout=10).status_code == 404


def test_update_404():
    assert requests.put(T + "/nope-" + RUN, json={"title": "x"}, timeout=10).status_code == 404


def test_delete_404():
    assert requests.delete(T + "/nope-" + RUN, timeout=10).status_code == 404


def test_comments_flow(task):
    base = f"{T}/{task['id']}/comments"
    r = requests.post(base, json={"author": "a", "body": "hello"}, timeout=10)
    assert r.status_code == 201, r.text
    cid = r.json()["id"]
    r = requests.get(base, timeout=10)
    assert r.status_code == 200 and r.json()["total"] >= 1
    assert requests.delete(f"{base}/{cid}", timeout=10).status_code in (200, 204)
    assert requests.delete(f"{base}/{cid}", timeout=10).status_code == 404


def test_comment_invalid(task):
    assert requests.post(f"{T}/{task['id']}/comments", json={"author": "a"}, timeout=10).status_code == 400


def test_comment_task_404():
    assert requests.get(f"{T}/nope-{RUN}/comments", timeout=10).status_code == 404
    assert requests.post(f"{T}/nope-{RUN}/comments", json={"author": "a", "body": "b"}, timeout=10).status_code == 404


def test_delete_then_404():
    t = requests.post(T, json={"title": f"{RUN}_del"}, timeout=10).json()
    assert requests.delete(f"{T}/{t['id']}", timeout=10).status_code in (200, 204)
    assert requests.get(f"{T}/{t['id']}", timeout=10).status_code == 404
