#!/usr/bin/env python3
"""serve-dev.py — `make serve`: the built web app (dist/) on localhost, with
the page's /api path forwarded to an mvd-api the way production does it
(Caddy's /api route, deploy/Caddyfile; the Netlify edge function,
netlify/edge-functions/api.js): /api/x becomes <MVD_API>x with the key
from MVD_API_KEY added, for the same allowlist of routes (test/api-routes
in mvd-web pins the three together).

    MVD_API      mvd-api's /v1/ base (default https://mvdanalyzer.com/v1/;
                 a local `mvd-api serve` without -auth-dir needs no key:
                 http://127.0.0.1:8090/v1/)
    MVD_API_KEY  the key to add (optional)

Without a reachable API or a valid key, hub demos are analysed in the
browser instead, and search goes to the hub directly.

It listens on localhost only: /api spends MVD_API_KEY, which nobody else on
the network should be able to.

usage: serve-dev.py DIR [PORT]
"""
import http.client
import http.server
import os
import re
import sys
import urllib.error
import urllib.request

GET_ROUTES = re.compile(r"^/api/(demos/[^/]+/(result|streams/positions|buckets|region-control|los|top-windows|top-kills|lives|highlights|events|backpacks|frags|weapon-pickups)|games/(search|[0-9]+))$")
POST_ROUTES = re.compile(r"^/api/demos/[^/]+/region-control$")
FORWARD = ("Accept", "Accept-Encoding", "Content-Type", "If-None-Match")

API = os.environ.get("MVD_API", "https://mvdanalyzer.com/v1/")
KEY = os.environ.get("MVD_API_KEY", "")


class Handler(http.server.SimpleHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith("/api/"):
            self.forward(GET_ROUTES)
        else:
            super().do_GET()

    def do_POST(self):
        self.forward(POST_ROUTES)

    def forward(self, routes):
        path, _, query = self.path.partition("?")
        if not routes.match(path):
            self.send_error(404)
            return
        body = None
        if self.command == "POST":
            try:
                body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
            except ValueError:
                self.send_error(400, "bad Content-Length")
                return
        req = urllib.request.Request(API + path[len("/api/"):] + ("?" + query if query else ""),
                                     data=body, method=self.command)
        for name in FORWARD:
            if self.headers.get(name):
                req.add_header(name, self.headers[name])
        if KEY:
            req.add_header("Authorization", "Bearer " + KEY)
        try:
            try:
                resp = urllib.request.urlopen(req, timeout=180)
            except urllib.error.HTTPError as e:
                resp = e
            data = resp.read()
        except (OSError, http.client.HTTPException) as e:
            self.send_error(502, "mvd-api unreachable: %s" % e)
            return
        self.send_response(resp.status)
        for name, value in resp.headers.items():
            if name.lower() not in ("content-length", "transfer-encoding", "connection", "date", "server"):
                self.send_header(name, value)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    os.chdir(sys.argv[1])
    port = int(sys.argv[2]) if len(sys.argv) > 2 else 8080
    print("Serving %s on http://localhost:%d (/api -> %s%s)"
          % (sys.argv[1], port, API, ", with MVD_API_KEY" if KEY else ""))
    http.server.ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()


if __name__ == "__main__":
    main()
