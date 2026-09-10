from __future__ import annotations

import json
import time
import unittest

from statsig_signer.watch import compare_fingerprints, fingerprint_from_html, should_backoff_repair


class WatchTest(unittest.TestCase):
    def test_html_fingerprint_extracts_curves_and_release(self) -> None:
        seg = {"color": [1, 2, 3, 4, 5, 6], "deg": 10, "bezier": [1, 2, 3, 4]}
        curves = [[seg, seg, seg, seg] for _ in range(4)]
        html = (
            'sentry-release=grok-web@abc1234 '
            'src="https://cdn.grok.com/_next/static/chunks/aaa.js" '
            '"curves":' + json.dumps(curves)
        )
        fp = fingerprint_from_html(html)
        self.assertEqual(fp["sentry_release"], "grok-web@abc1234")
        self.assertTrue(fp["curves_hash"])
        self.assertTrue(fp["chunks_hash"])
        self.assertEqual(fp["path_count"], 4)
        self.assertEqual(fp["script_count"], 1)

    def test_compare_detects_curves_and_release_change(self) -> None:
        prev = {"sentry_release": "grok-web@aaa", "curves_hash": "h1", "chunks_hash": "c1", "source": "html"}
        same = compare_fingerprints(prev, dict(prev))
        self.assertEqual(same, [])
        changed = compare_fingerprints(
            prev,
            {"sentry_release": "grok-web@bbb", "curves_hash": "h2", "chunks_hash": "c2", "source": "html"},
        )
        self.assertIn("sentry_release", changed)
        self.assertIn("curves_hash", changed)
        self.assertIn("chunks", changed)
        self.assertEqual(compare_fingerprints(None, prev), ["first_seen"])

    def test_compare_ignores_html_vs_capture_url_lists(self) -> None:
        html_fp = {
            "sentry_release": "grok-web@abc",
            "curves_hash": "h1",
            "chunks_hash": "html-chunks",
            "source": "html",
        }
        capture_fp = {
            "sentry_release": "grok-web@abc",
            "curves_hash": "h1",
            "chunks_hash": "capture-chunks",
            "source": "capture",
            "signer_urls": ["https://cdn.grok.com/_next/static/chunks/signer.js"],
        }
        self.assertEqual(compare_fingerprints(html_fp, capture_fp), [])
        self.assertEqual(compare_fingerprints(capture_fp, html_fp), [])

    def test_repair_backs_off_after_failed_unchanged_tick(self) -> None:
        self.assertFalse(should_backoff_repair(None, True))
        self.assertFalse(should_backoff_repair({"repair_ok": False}, True))
        self.assertFalse(should_backoff_repair({"repair_ok": True}, False))
        self.assertFalse(should_backoff_repair({"repair_ok": False}, False))
        self.assertTrue(should_backoff_repair({"repair_ok": False, "repair_attempted_at": time.time()}, False))
