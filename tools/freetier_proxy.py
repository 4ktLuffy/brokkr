#!/usr/bin/env python3
"""An OpenAI-compatible endpoint on localhost that spends a free tier through
freetier (github.com/4ktLuffy/freetier).

Brokkr's agent runs inside the Lima VM and never holds a provider key. It points
--model-url at this proxy, which runs on the Mac, holds the key, and forwards each
chat completion through freetier's paced client:

- calls are paced to the provider's published limits and booked in freetier's
  machine-wide ledger, shared with every other project on this machine that uses
  the same key;
- when the allowance is gone, freetier parks instead of retrying into 429s, and the
  proxy answers HTTP 429 with {"error": {"type": "budget_parked", ...}} and an
  X-Brokkr-Budget-Parked header naming the UTC time the allowance returns. Brokkr
  records that as an infra error and the eval stops; it is never scored as the
  agent failing.

The key comes from BROKKR_UPSTREAM_API_KEY, or else from the macOS Keychain item
named by BROKKR_KEYCHAIN_SERVICE (default "brokkr-upstream"):

    security add-generic-password -s brokkr-upstream -a "$USER" -w   # prompts for it

The key is never logged, never written to disk by this proxy, and never sent to
the client. The proxy binds 127.0.0.1 only.

    BROKKR_UPSTREAM_URL=https://openrouter.ai/api/v1 tools/freetier_proxy.py
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer, ThreadingHTTPServer
from typing import Any

from freetier.ledger import BudgetParked, ProviderNotConfigured
from freetier.openai_compat import paced_openai_client

# Fields forwarded to the provider. Anything else in the request is dropped, so a
# client cannot switch on streaming or smuggle provider-specific options.
FORWARD = {"model", "messages", "tools", "tool_choice", "temperature", "max_tokens", "top_p", "seed", "stop"}


def load_key() -> str:
    key = os.environ.get("BROKKR_UPSTREAM_API_KEY", "")
    if key:
        return key
    service = os.environ.get("BROKKR_KEYCHAIN_SERVICE", "brokkr-upstream")
    try:
        out = subprocess.run(
            ["security", "find-generic-password", "-s", service, "-w"],
            capture_output=True, text=True, check=True,
        )
    except (OSError, subprocess.CalledProcessError):
        sys.exit(f"no key: set BROKKR_UPSTREAM_API_KEY or add Keychain item {service!r} "
                 f"(security add-generic-password -s {service} -a \"$USER\" -w)")
    return out.stdout.strip()


def make_handler(client: Any) -> type[BaseHTTPRequestHandler]:
    class Handler(BaseHTTPRequestHandler):
        server_version = "brokkr-freetier-proxy"

        def log_message(self, fmt: str, *args: Any) -> None:  # request lines only; never headers
            sys.stderr.write("proxy: " + (fmt % args) + "\n")

        def reply(self, status: int, body: dict[str, Any], headers: dict[str, str] | None = None) -> None:
            raw = json.dumps(body).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            for k, v in (headers or {}).items():
                self.send_header(k, v)
            self.end_headers()
            self.wfile.write(raw)

        def do_GET(self) -> None:
            if self.path == "/healthz":
                self.reply(200, {"ok": True})
            else:
                self.reply(404, {"error": {"type": "not_found"}})

        def do_POST(self) -> None:
            if self.path.rstrip("/") not in ("/v1/chat/completions", "/chat/completions"):
                self.reply(404, {"error": {"type": "not_found"}})
                return
            try:
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
            except (ValueError, json.JSONDecodeError):
                self.reply(400, {"error": {"type": "bad_request", "message": "body is not JSON"}})
                return
            kwargs = {k: v for k, v in body.items() if k in FORWARD and v is not None}
            if kwargs.get("max_tokens") == 0:
                kwargs.pop("max_tokens")
            try:
                resp = client.chat.completions.create(**kwargs)
            except BudgetParked as p:
                self.reply(429, {"error": {"type": "budget_parked", "message": str(p), **p.to_details()}},
                           {"X-Brokkr-Budget-Parked": p.resume_at.isoformat()})
                return
            except ProviderNotConfigured as e:
                self.reply(400, {"error": {"type": "provider_not_configured", "message": str(e)}})
                return
            except Exception as e:  # provider errors: pass the status through, never the request
                status = getattr(e, "status_code", None) or 502
                self.reply(int(status), {"error": {"type": "upstream_error", "message": f"{type(e).__name__}: {e}"[:500]}})
                return
            self.reply(200, resp.model_dump(mode="json", exclude_none=True))

    return Handler


class Server(ThreadingHTTPServer):
    """One thread per request. A single-threaded server serialises every agent
    behind whichever request is in flight, and its default listen backlog (5)
    drops connections once more agents than that are waiting (seen: "dial tcp
    ... i/o timeout" with 7 concurrent agents). freetier's Ledger and Pacer
    share one threading.Lock and are safe across threads."""

    daemon_threads = True
    request_queue_size = 128


def make_server(port: int, client: Any) -> HTTPServer:
    return Server(("127.0.0.1", port), make_handler(client))


def main() -> None:
    upstream = os.environ.get("BROKKR_UPSTREAM_URL", "")
    if not upstream:
        sys.exit("set BROKKR_UPSTREAM_URL, e.g. https://openrouter.ai/api/v1")
    port = int(os.environ.get("BROKKR_PROXY_PORT", "11500"))
    client = paced_openai_client(upstream, load_key())
    server = make_server(port, client)
    print(f"brokkr freetier proxy: 127.0.0.1:{port} -> {upstream}", file=sys.stderr)
    server.serve_forever()


if __name__ == "__main__":
    main()
