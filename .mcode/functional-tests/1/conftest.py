import json, os, pytest, requests
BASE = os.environ.get("BASE_URL", "http://localhost:8080")
LOG = "/tmp/ft_last.json"
_rec = {}

class Client:
    def __init__(self): self.last = None
    def req(self, method, path, **kw):
        r = requests.request(method, BASE + path, timeout=10, **kw)
        self.last = r
        return r

@pytest.fixture
def api(request):
    c = Client()
    yield c
    if c.last is not None:
        _rec[request.node.name] = {"method": c.last.request.method, "endpoint": c.last.request.path_url,
            "body": (c.last.request.body.decode() if isinstance(c.last.request.body, bytes) else c.last.request.body),
            "status": c.last.status_code, "output": c.last.text}
        json.dump(_rec, open(LOG, "w"))

@pytest.fixture(autouse=True)
def _health():
    assert requests.get(BASE + "/health", timeout=5).status_code == 200
