from __future__ import annotations

import unittest

from statsig_signer.capture import HOOK_JS, PROBE_JS, _init_script, verify_record


class CaptureHookTest(unittest.TestCase):
    def test_init_script_is_a_statement_not_an_arrow_function(self) -> None:
        body = _init_script()
        self.assertFalse(body.lstrip().startswith("() =>"))
        self.assertIn("crypto.subtle.digest", body)
        self.assertIn("obfiowerehiring", body)
        self.assertIn("Number.prototype.toString", body)
        self.assertIn("hexFromToString", body)
        self.assertIn("Element.prototype.animate", body)

    def test_probe_harvests_getanimations_as_seek_fallback(self) -> None:
        self.assertIn("document.getAnimations", PROBE_JS)
        self.assertIn("/rest/modes", PROBE_JS)

    def test_verify_record_omits_seed(self) -> None:
        record = verify_record(
            {
                "ok": True,
                "url": "https://grok.com/imagine",
                "seed": "secret-seed-value",
                "hex": "abc",
                "hex_from_tostring": "abc",
                "hex_agree": True,
                "seeks": [{"value": 240, "via": "getAnimations"}],
                "anims": [{"via": "animate"}],
            }
        )
        encoded = str(record)
        self.assertNotIn("secret-seed-value", encoded)
        self.assertEqual(record["seed_len"], 17)
        self.assertTrue(record["hex_agree"])
        self.assertEqual(record["seeks"][0]["via"], "getAnimations")
