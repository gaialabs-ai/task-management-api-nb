import json, os, uuid, requests, pytest

BASE = "http://localhost:" + os.environ.get("PORT", "8080")
API = BASE + "/api/v1"
LOG = "/tmp/ft_log.json"
RUN = "ftrun" + uuid.uuid4().hex[:8]


def rec(name, r):
    try:
        d = json.load(open(LOG))
    except Exception:
        d = {}
    d[name] = {"status": r.status_code, "body": r.text[:1500]}
    json.dump(d, open(LOG, "w"))


def mk(title, **kw):
    r = requests.post(API + "/tasks", json={"title": title, "tags": [RUN], **kw}, timeout=10)
    assert r.status_code == 201, r.text
    return r.json()


@pytest.fixture(scope="module")
def seeded():
    ids = []
    t = [
        mk(RUN + " alpha", priority="low", assigned_to=RUN + "u", due_date="2030-01-03T00:00:00Z", tags=[RUN, "x"]),
        mk(RUN + " bravo", priority="critical", due_date="2030-01-01T00:00:00Z", tags=[RUN, "y"]),
        mk(RUN + " charlie", priority="high", tags=[RUN, "x", "y"]),
        mk(RUN + " delta", priority="medium", due_date="2030-01-02T00:00:00Z", description=RUN + "needle"),
        mk(RUN + " echo", priority="medium"),
    ]
    ids = [x["id"] for x in t]
    yield t
    for i in ids:
        requests.delete(API + "/tasks/" + i, timeout=10)


def lst(**params):
    params.setdefault("tags", RUN)
    return requests.get(API + "/tasks", params=params, timeout=10)


def test_health():
    r = requests.get(BASE + "/health", timeout=10); rec("test_health", r)
    assert r.status_code == 200 and r.json()["status"] == "healthy"


def test_create_get_update_delete():
    r = requests.post(API + "/tasks", json={"title": RUN + " crud", "priority": "high"}, timeout=10)
    rec("test_create_task", r)
    assert r.status_code == 201
    t = r.json(); assert t["status"] == "pending" and t["tags"] == [] and r.headers["Location"].endswith(t["id"])
    r = requests.get(API + "/tasks/" + t["id"], timeout=10); rec("test_get_task", r)
    assert r.status_code == 200 and r.json()["title"] == RUN + " crud"
    r = requests.put(API + "/tasks/" + t["id"], json={"title": "new", "tags": ["a"]}, timeout=10); rec("test_update_task", r)
    assert r.status_code == 200 and r.json()["title"] == "new" and r.json()["tags"] == ["a"]
    r = requests.patch(API + "/tasks/" + t["id"] + "/status", json={"status": "completed"}, timeout=10); rec("test_patch_status", r)
    assert r.status_code == 200 and r.json()["status"] == "completed" and r.json()["completed_at"]
    r = requests.delete(API + "/tasks/" + t["id"], timeout=10); rec("test_delete_task", r)
    assert r.status_code == 204
    r = requests.get(API + "/tasks/" + t["id"], timeout=10); rec("test_get_deleted_404", r)
    assert r.status_code == 404 and r.json()["error"]["code"] == "NOT_FOUND"


def test_errors():
    r = requests.post(API + "/tasks", json={"description": "x"}, timeout=10); rec("test_create_missing_title_400", r)
    assert r.status_code == 400 and r.json()["error"]["code"] == "VALIDATION_ERROR"
    r = requests.put(API + "/tasks/nope", json={"title": "x"}, timeout=10); rec("test_update_unknown_404", r)
    assert r.status_code == 404
    r = requests.patch(API + "/tasks/nope/status", json={"status": "bogus"}, timeout=10); rec("test_patch_invalid_status_400", r)
    assert r.status_code == 400
    r = requests.delete(API + "/tasks/nope", timeout=10); rec("test_delete_unknown_404", r)
    assert r.status_code == 404


def test_filter_tags(seeded):
    r = lst(tags=RUN + ",x"); rec("test_filter_tags_all", r)
    assert r.status_code == 200 and r.json()["total_count"] == 2
    r = lst(tags=RUN + ", y"); assert r.json()["total_count"] == 2
    r = lst(tags=RUN + ",nonexistent"); assert r.json()["total_count"] == 0


def test_filter_status_assigned(seeded):
    requests.patch(API + "/tasks/" + seeded[4]["id"] + "/status", json={"status": "on_hold"}, timeout=10)
    r = lst(status="on_hold"); rec("test_filter_status", r)
    assert r.json()["total_count"] == 1
    r = lst(assigned_to=RUN + "u"); rec("test_filter_assigned_to", r)
    assert r.json()["total_count"] == 1 and r.json()["tasks"][0]["title"].endswith("alpha")


def test_sorts(seeded):
    def titles(o):
        r = lst(sort_order=o); rec("test_sort_" + o, r)
        return [t["title"].split()[-1] for t in r.json()["tasks"]]
    assert titles("created_at_asc") == ["alpha", "bravo", "charlie", "delta", "echo"]
    assert titles("created_at_desc") == ["echo", "delta", "charlie", "bravo", "alpha"]
    pa = titles("priority_asc"); assert pa[0] == "alpha" and pa[-1] == "bravo"
    pd = titles("priority_desc"); assert pd[0] == "bravo" and pd[1] == "charlie" and pd[-1] == "alpha"
    assert sorted(pd[2:4]) == ["delta", "echo"]
    da = titles("due_date_asc"); assert da[:3] == ["bravo", "delta", "alpha"]
    dd = titles("due_date_desc"); assert dd[:3] == ["alpha", "delta", "bravo"] and sorted(dd[3:]) == ["charlie", "echo"]


def test_pagination(seeded):
    r1 = lst(page_size=2, sort_order="created_at_asc"); rec("test_pagination_page1", r1)
    j = r1.json(); assert len(j["tasks"]) == 2 and j["total_count"] == 5 and j["next_page_token"] == "2"
    j2 = lst(page_size=2, page_token="2", sort_order="created_at_asc").json()
    j3 = lst(page_size=2, page_token="4", sort_order="created_at_asc"); rec("test_pagination_last", j3)
    j3 = j3.json()
    assert len(j3["tasks"]) == 1 and "next_page_token" not in j3
    ids = [t["id"] for t in j["tasks"] + j2["tasks"] + j3["tasks"]]
    assert ids == [t["id"] for t in seeded]


def test_pagination_invalid():
    for v in ("0", "101", "abc"):
        r = lst(page_size=v); rec("test_page_size_invalid", r)
        assert r.status_code == 400


def test_search(seeded):
    r = requests.get(API + "/tasks/search", params={"q": RUN + "needle"}, timeout=10); rec("test_search", r)
    assert r.status_code == 200 and r.json()["total_count"] == 1
    r = requests.get(API + "/tasks/search", params={"q": RUN, "page_size": 2}, timeout=10)
    assert len(r.json()["tasks"]) == 2 and r.json()["total_count"] == 5
    assert r.json()["tasks"][0]["title"].startswith(RUN)  # title matches rank first
    r = requests.get(API + "/tasks/search", timeout=10); rec("test_search_missing_q_400", r)
    assert r.status_code == 400


def test_stats(seeded):
    r = requests.get(API + "/tasks/stats", timeout=10); rec("test_stats", r)
    j = r.json()
    assert r.status_code == 200 and j["total"] >= 5 and sum(j["by_status"].values()) == j["total"]
    assert sum(j["by_priority"].values()) == j["total"]


def test_comments(seeded):
    tid = seeded[0]["id"]
    r = requests.post(API + f"/tasks/{tid}/comments", json={"author": "bob", "body": "hi"}, timeout=10); rec("test_add_comment", r)
    assert r.status_code == 201; c = r.json(); assert c["task_id"] == tid and c["author"] == "bob"
    r = requests.get(API + f"/tasks/{tid}/comments", timeout=10); rec("test_list_comments", r)
    assert r.status_code == 200 and r.json()["total"] == 1
    r = requests.post(API + f"/tasks/{tid}/comments", json={"author": "bob"}, timeout=10); rec("test_add_comment_invalid_400", r)
    assert r.status_code == 400
    r = requests.post(API + "/tasks/nope/comments", json={"author": "a", "body": "b"}, timeout=10); rec("test_add_comment_unknown_task_404", r)
    assert r.status_code == 404
    r = requests.delete(API + f"/tasks/{tid}/comments/nope", timeout=10); rec("test_delete_unknown_comment_404", r)
    assert r.status_code == 404
    r = requests.delete(API + f"/tasks/{tid}/comments/{c['id']}", timeout=10); rec("test_delete_comment", r)
    assert r.status_code == 204
    assert requests.get(API + f"/tasks/{tid}/comments", timeout=10).json()["total"] == 0
    r = requests.get(API + "/tasks/nope/comments", timeout=10); rec("test_list_comments_unknown_404", r)
    assert r.status_code == 404
