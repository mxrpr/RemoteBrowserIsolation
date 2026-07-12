#!/usr/bin/env python3
"""Tiny local HTTP fixture server for the video-mode e2e tests.

Serves /keytest.html?token=X, a page whose only job is to beacon back to this
same server (GET /beacon?token=X) whenever a real 'keydown' DOM event reaches
it. Since RBI's headless-Chromium session replays input via Playwright's real
Keyboard API (page.Keyboard.DownAsync), a keydown that actually reaches this
page is proof the server replayed it -- and VideoNoInput's server-side
keyboard drop (InputEventForwarder) means no such event is ever dispatched,
so no beacon arrives. This gives the e2e suite a real, non-visual signal to
tell VideoAllowInput and VideoNoInput apart without decoding video frames.

Usage: keytest_server.py <port>
Then: GET /results -> JSON list of tokens that have beaconed a keydown.
"""
import http.server
import json
import sys
import urllib.parse

seen_tokens = set()

PAGE_TEMPLATE = """<!doctype html>
<html><body>
<p>keytest fixture</p>
<script>
document.addEventListener('keydown', function () {
  fetch('/beacon?token=' + encodeURIComponent(new URLSearchParams(location.search).get('token')));
});
</script>
</body></html>
"""


class Handler(http.server.BaseHTTPRequestHandler):
    # Serves the keytest page, records keydown beacons, and reports results --
    # the three endpoints this fixture needs, dispatched by path.
    def do_GET(self):
        parsed = urllib.parse.urlparse(self.path)
        if parsed.path == "/keytest.html":
            body = PAGE_TEMPLATE.encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        elif parsed.path == "/beacon":
            token = urllib.parse.parse_qs(parsed.query).get("token", [""])[0]
            if token:
                seen_tokens.add(token)
            self.send_response(204)
            self.end_headers()
        elif parsed.path == "/results":
            body = json.dumps(sorted(seen_tokens)).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        else:
            self.send_response(404)
            self.end_headers()

    # Silence default request logging -- the orchestrating bash script doesn't
    # need per-request noise interleaved with the rest of the test output.
    def log_message(self, format, *args):
        pass


if __name__ == "__main__":
    port = int(sys.argv[1])
    server = http.server.HTTPServer(("127.0.0.1", port), Handler)
    server.serve_forever()
