#!/usr/bin/env python3
"""Capture map-tab screenshots from the built web app.

Drives dist/ in headless chromium: loads one demo through the file input,
parks the clock at fixed times, and shoots the map canvas in a fixed set of
view states. Used to pin the map renderer's output across the extraction of
the renderer into mvd-map-view — capture with --out baseline before the
move, --out after once it builds, then compare.

    python3 mvd-web/test/mapshot.py --demo mvd-analytics/testdata/cache/212260.mvd.gz \
        --out /tmp/shots/baseline

Requires playwright + chromium (already provisioned on the dev box) and a
current dist/ (`make build`).
"""

import argparse
import http.server
import os
import pathlib
import socketserver
import threading

from playwright.sync_api import sync_playwright

REPO = pathlib.Path(__file__).resolve().parents[2]

# Each state is (name, javascript[, wait]). The JS runs in page context with
# the clock already parked; it mutates view state and returns once the map has
# been redrawn. Buttons are clicked rather than state poked directly so the
# capture exercises the same paths a user does. `wait` is an optional page
# condition polled before the shot — the LOS/PVS toggles kick off a lazy,
# multi-second worker raycast on first use, and shooting before it lands made
# those shots depend on scheduler luck (the only nondeterminism ever seen in
# this harness).
STATES = [
    ("default3d", ""),
    ("topdown", "document.getElementById('map-3d-toggle').click()"),
    ("trails", "document.getElementById('map-trails-all').click()"),
    ("viewarrows", "document.getElementById('map-view-arrows').click()"),
    ("velarrows", "document.getElementById('map-vel-arrows').click()"),
    ("los", "document.getElementById('map-los').click()",
     "mapState && !mapState.losPending"),
    ("pvs", "document.getElementById('map-pvs').click()",
     "mapState && !mapState.losPending"),
    ("learn", "document.getElementById('map-learn-toggle').click()"),
    ("rotated", "setMapCamera(1.9, 0.6); renderMap(mapState.currentTime)"),
    ("zoomed", "_wtc.zoomK = 2.5; markMapDirty(); renderMap(mapState.currentTime)"),
]


class _Quiet(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *a):
        pass


def serve(directory, port):
    handler = lambda *a, **kw: _Quiet(*a, directory=str(directory), **kw)
    socketserver.TCPServer.allow_reuse_address = True
    # port 0 lets the OS pick a free one — a fixed port collides with a
    # previous run's socket still in TIME_WAIT.
    httpd = socketserver.TCPServer(("127.0.0.1", port), handler)
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    return httpd, httpd.server_address[1]


def interact(page, out_dir, label, t):
    """Shots driven through real mouse input, not state pokes.

    These cover what the JS states cannot: the orbit-pivot pick and the
    zoom anchor both invert the projection (canvasToWorld / canvasToView),
    so a regression there shows up here and nowhere else. The steps are
    cumulative and end with Reset view, which restores the camera for
    whatever runs next.
    """
    box = page.locator("#map-canvas").bounding_box()
    cx, cy = box["x"] + box["width"] / 2, box["y"] + box["height"] / 2

    def shot(name):
        page.wait_for_timeout(80)
        page.locator("#map-canvas").screenshot(path=str(out_dir / f"{label}-t{t}-{name}.png"))

    page.mouse.move(cx, cy)
    page.mouse.down()
    page.mouse.move(cx + 90, cy - 40, steps=4)
    page.mouse.up()
    shot("i-pan")

    # Right-drag orbits: re-centres the pivot on the view centre, then applies
    # absolute yaw/pitch deltas from the drag start.
    page.mouse.move(cx, cy)
    page.mouse.down(button="right")
    page.mouse.move(cx + 120, cy + 60, steps=6)
    page.mouse.up(button="right")
    shot("i-orbit")

    # Wheel zoom anchors on the cursor, off-centre so a broken inverse moves
    # the anchor point visibly.
    page.mouse.move(cx - 150, cy + 100)
    page.mouse.wheel(0, -600)
    shot("i-wheelzoom")

    page.click("#map-reset-view")
    shot("i-resetview")


def capture(page, out_dir, label, times):
    for t in times:
        page.evaluate(f"setCurrentTime({t})")
        interact(page, out_dir, label, t)
        for name, js, *rest in STATES:
            if js:
                page.evaluate(js)
                page.wait_for_timeout(120)
            if rest:
                page.wait_for_function(rest[0], timeout=120_000)
                page.wait_for_timeout(120)
            page.evaluate("renderMap(mapState.currentTime)")
            page.wait_for_timeout(60)
            path = out_dir / f"{label}-t{t}-{name}.png"
            page.locator("#map-canvas").screenshot(path=str(path))
            if js:  # restore: every state above is a toggle
                page.evaluate(js)
                page.wait_for_timeout(60)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--demo", required=True, help="path to a .mvd/.mvd.gz")
    ap.add_argument("--out", required=True, help="output directory")
    ap.add_argument("--dist", default=str(REPO / "dist"))
    ap.add_argument("--port", type=int, default=0, help="0 = OS-assigned")
    ap.add_argument(
        "--no-geometry",
        action="store_true",
        help="block the maps/<map>.json fetch, exercising the convex-hull "
        "fallback path used by maps with no pre-generated BSP geometry",
    )
    ap.add_argument(
        "--times",
        default="60,300,600",
        help="comma-separated match-relative SECONDS to park the clock at "
        "(the frontend clock is seconds, unlike the ms-native API)",
    )
    args = ap.parse_args()

    out_dir = pathlib.Path(args.out)
    out_dir.mkdir(parents=True, exist_ok=True)
    times = [int(t) for t in args.times.split(",")]
    label = pathlib.Path(args.demo).name.split(".")[0]

    httpd, port = serve(args.dist, args.port)
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch()
            page = browser.new_page(viewport={"width": 1600, "height": 1100})
            page.on("console", lambda m: m.type == "error" and print("[console]", m.text))
            if args.no_geometry:
                page.route("**/maps/*.json", lambda route: route.abort())
            page.goto(f"http://127.0.0.1:{port}/index.html?tab=map")
            page.wait_for_selector("#file-input", state="attached")
            # The app rejects a file dropped before the WASM module is live
            # ("Analyzer is still loading"), so gate on the worker's ready flag.
            page.wait_for_function("typeof wasmReady !== 'undefined' && wasmReady", timeout=60_000)
            page.set_input_files("#file-input", os.path.abspath(args.demo))
            # displayResults() drops the no-demo class, but two inputs the map
            # needs land later: the BSP geometry on its own fetch, and the 50 ms
            # bucket view on the worker's deferred 'buckets' message
            # (applyDeferredBuckets). Without the latter the map draws a world
            # with no players in it.
            page.wait_for_selector("body:not(.no-demo)", timeout=180_000)
            if not args.no_geometry:
                page.wait_for_function("mapState && mapState.mapGeometry", timeout=60_000)
            page.wait_for_function("timelineState && timelineState.bucketView", timeout=120_000)
            page.wait_for_timeout(500)
            capture(page, out_dir, label, times)
            browser.close()
    finally:
        httpd.shutdown()

    print(f"wrote {len(list(out_dir.glob('*.png')))} shots to {out_dir}")


if __name__ == "__main__":
    main()
