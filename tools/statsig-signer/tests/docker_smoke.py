"""Offline Docker smoke fixture: real Chromium, synthetic Grok page, real watch/publish loop."""
from __future__ import annotations

import json
import os
from pathlib import Path
from unittest.mock import patch

from playwright.sync_api import sync_playwright

from statsig_signer.algorithm import compute_hex, curves_hash, decode_seed
from statsig_signer.capture import _init_script
from statsig_signer.htmlutil import curve_group_to_path, extract_curve_paths
from statsig_signer.store import Store
from statsig_signer.watch import watch_loop


def browser_sample() -> dict:
    store = Store()
    seed = store.pair.seed
    segment = {"color": [12, 32, 96, 48, 128, 64], "deg": 90, "bezier": [16, 64, 96, 128]}
    groups = [[segment for _ in range(16)] for _ in range(4)]
    paths = [curve_group_to_path(group) for group in groups]
    expected = compute_hex(decode_seed(seed), paths, store.formula)
    html = (
        '<meta name="grok-site-verification" content="' + seed + '">'
        '<script type="application/json">' + json.dumps({"curves": groups}) + '</script>'
        '<script>crypto.subtle.digest("SHA-256", new TextEncoder().encode('
        + json.dumps("obfiowerehiring" + expected) + '));</script>'
    )
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(executable_path=os.environ["PLAYWRIGHT_CHROMIUM"])
        try:
            context = browser.new_context()
            context.add_init_script(_init_script())
            context.route("**/*", lambda route: route.fulfill(status=200, content_type="text/html", body=html))
            page = context.new_page()
            page.goto("https://grok.com/imagine")
            digest = page.evaluate("window.__sig.digest")
            assert digest["seed"] == seed and digest["hex"] == expected
            assert extract_curve_paths(page.content()) == paths
        finally:
            browser.close()
    print("PASS: non-root Chromium and real digest capture hook", flush=True)
    return {"ok": True, "seed": seed, "hex": expected, "paths": paths, "browser": "local", "url": "https://grok.com/imagine"}


if __name__ == "__main__":
    captured = browser_sample()
    directory = Path(os.environ["STATSIG_STATE_DIR"])

    def probe(**kwargs):
        return {
            "ok": True,
            "fingerprint": {
                "source": "html",
                "sentry_release": "smoke-failure" if (directory / "smoke-fail").exists() else "smoke-success",
                "curves_hash": curves_hash(captured["paths"]),
            },
            "hex_match": {"ok": None},
            "observed_at": "offline-smoke",
        }

    def capture(**kwargs):
        if (directory / "smoke-fail").exists():
            raise RuntimeError("offline smoke: simulated capture outage")
        return dict(captured)

    with patch("statsig_signer.watch.probe", probe), patch("statsig_signer.agent.capture", capture):
        watch_loop(interval=30, repair=True)
