"""Tests for tools/freetier_proxy.py. No key, no network: every outbound request is
sent to a dead proxy address, so a test that reaches the network fails loudly
instead of spending anyone's allowance.

    tools/.venv/bin/python -m unittest tools/test_freetier_proxy.py -v
"""

from __future__ import annotations

import json
import os
import sys
import tempfile
import threading
import unittest
import urllib.error
import urllib.request
from datetime import timedelta
from http.server import HTTPServer
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

# Before anything imports httpx: route all outbound traffic to a closed port.
os.environ["HTTPS_PROXY"] = os.environ["HTTP_PROXY"] = "http://127.0.0.1:9"
os.environ.pop("NO_PROXY", None)

from freetier.openai_compat import paced_openai_client  # noqa: E402
from freetier.ledger import Ledger, utcnow  # noqa: E402

import freetier_proxy  # noqa: E402

UPSTREAM = "https://openrouter.ai/api/v1"
MODEL = "meta-llama/llama-3.3-70b-instruct:free"


class ProxyTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.ledger = Ledger(Path(self.tmp.name) / "ledger.json")
        client = paced_openai_client(UPSTREAM, "dummy-key-not-real", ledger=self.ledger, timeout=5)
        self.server = HTTPServer(("127.0.0.1", 0), freetier_proxy.make_handler(client))
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.server.server_port}/v1/chat/completions"

    def tearDown(self) -> None:
        self.server.shutdown()
        self.server.server_close()
        self.tmp.cleanup()

    def post(self, body: dict) -> tuple[int, dict, dict]:
        req = urllib.request.Request(self.url, json.dumps(body).encode(), {"Content-Type": "application/json"})
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))  # the test itself talks direct
        try:
            with opener.open(req, timeout=30) as r:
                return r.status, json.loads(r.read()), dict(r.headers)
        except urllib.error.HTTPError as e:
            return e.code, json.loads(e.read()), dict(e.headers)

    def test_parked_allowance_answers_429_without_network(self) -> None:
        until = utcnow() + timedelta(hours=3)
        self.ledger.park("openrouter", MODEL, "test: allowance spent", until)
        status, body, headers = self.post({"model": MODEL, "messages": [{"role": "user", "content": "hi"}]})
        self.assertEqual(status, 429)
        self.assertEqual(body["error"]["type"], "budget_parked")
        self.assertEqual(body["error"]["provider"], "openrouter")
        self.assertIn("X-Brokkr-Budget-Parked", headers)
        self.assertNotIn("dummy-key", json.dumps(body))

    def test_negative_control_unparked_call_is_not_reported_as_parked(self) -> None:
        # Nothing parked: the call goes for the network, hits the dead proxy and fails.
        # It must surface as an upstream error, never as budget_parked.
        status, body, _ = self.post({"model": MODEL, "messages": [{"role": "user", "content": "hi"}]})
        self.assertNotEqual(body["error"]["type"], "budget_parked")
        self.assertNotEqual(status, 200)
        self.assertNotIn("dummy-key", json.dumps(body))

    def test_unknown_provider_is_refused(self) -> None:
        client = paced_openai_client("https://api.example.invalid/v1", "k", ledger=self.ledger)
        server = HTTPServer(("127.0.0.1", 0), freetier_proxy.make_handler(client))
        threading.Thread(target=server.serve_forever, daemon=True).start()
        try:
            self.url = f"http://127.0.0.1:{server.server_port}/v1/chat/completions"
            status, body, _ = self.post({"model": "m", "messages": [{"role": "user", "content": "hi"}]})
            self.assertEqual((status, body["error"]["type"]), (400, "provider_not_configured"))
        finally:
            server.shutdown()
            server.server_close()

    def test_concurrent_requests_are_served_in_parallel(self) -> None:
        # A threaded server answers many clients at once, even past the old
        # backlog of 5. Use a parked allowance so no request leaves the machine.
        import concurrent.futures, time
        self.ledger.park("openrouter", MODEL, "test", utcnow() + timedelta(hours=1))
        server = freetier_proxy.make_server(0, paced_openai_client(UPSTREAM, "dummy-key-not-real", ledger=self.ledger, timeout=5))
        threading.Thread(target=server.serve_forever, daemon=True).start()
        try:
            self.url = f"http://127.0.0.1:{server.server_port}/v1/chat/completions"
            t0 = time.time()
            with concurrent.futures.ThreadPoolExecutor(20) as ex:
                results = list(ex.map(lambda _: self.post({"model": MODEL, "messages": [{"role": "user", "content": "hi"}]}), range(20)))
            self.assertEqual({r[0] for r in results}, {429})
            self.assertLess(time.time() - t0, 10)
        finally:
            server.shutdown()
            server.server_close()

    def test_streaming_is_not_forwarded(self) -> None:
        self.assertNotIn("stream", freetier_proxy.FORWARD)


if __name__ == "__main__":
    unittest.main()
