from __future__ import annotations

import json
import unittest

from statsig_signer.watch import compare_fingerprints, fingerprint_from_html, should_backoff_repair


class WatchTest(unittest.TestCase):
    def test_html_fingerprint_extracts_curves_and_release(self) -> None:
        seg = {"color": [1, 2, 3, 4, 5, 6], "deg": 10, "bezier": [1, 2, 3, 4]}
        curves = [[seg, seg, seg, seg] for _ in range(4)]
        html = 'sentry-release=grok-web@abc1234 "curves":' + json.dumps(curves)
        fp = fingerprint_from_html(html)
        self.assertEqual(fp["sentry_release"], "grok-web@abc1234")
        self.assertTrue(fp["curves_hash"])
        self.assertEqual(fp["path_count"], 4)

    def test_compare_detects_curves_and_release_change(self) -> None:
        prev = {"sentry_release": "grok-web@aaa", "curves_hash": "h1", "script_names": ["a.js"]}
        same = compare_fingerprints(prev, dict(prev))
        self.assertEqual(same, [])
        changed = compare_fingerprints(prev, {"sentry_release": "grok-web@bbb", "curves_hash": "h2", "script_names": ["b.js"]})
        self.assertIn("sentry_release", changed)
        self.assertIn("curves_hash", changed)
        self.assertIn("chunks", changed)
        self.assertEqual(compare_fingerprints(None, prev), ["first_seen"])

    def test_repair_backs_off_after_failed_unchanged_tick(self) -> None:
        self.assertFalse(should_backoff_repair(None, True))
        self.assertFalse(should_backoff_repair({"repair_ok": False}, True))
        self.assertFalse(should_backoff_repair({"repair_ok": True}, False))
        self.assertTrue(should_backoff_repair({"repair_ok": False}, False))
