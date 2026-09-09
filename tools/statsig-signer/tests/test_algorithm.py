from __future__ import annotations

import json
import time
import unittest
from pathlib import Path

from statsig_signer.algorithm import (
    Formula,
    aligned_seek,
    build_statsig,
    compute_hex,
    decode_seed,
    inspect_statsig_id,
    number_to_hex,
    sign_with_meta,
    valid_statsig_id,
)

ROOT = Path(__file__).resolve().parents[1]
PAIR = json.loads((ROOT / "data" / "pair.json").read_text(encoding="utf-8"))


class CubicBezierTest(unittest.TestCase):
    def test_degenerate_controls_terminate(self) -> None:
        from statsig_signer.algorithm import cubic_bezier_y

        start = time.time()
        cubic_bezier_y(0, 0, 0, 0, 0.5)
        cubic_bezier_y(1, 1, 1, 1, 0.3)
        cubic_bezier_y(0.5, 0.5, 0.5, 0.5, 0.7)
        self.assertLess(time.time() - start, 0.05)


class NumberToHexTest(unittest.TestCase):
    def test_matches_js(self) -> None:
        self.assertEqual(number_to_hex(185), "b9")
        self.assertEqual(number_to_hex(0.24), "0.3d70a3d70a3d7")
        self.assertEqual(number_to_hex(0.97), "0.f851eb851eb85")
        self.assertEqual(number_to_hex(0.01), "0.028f5c28f5c28f6")
        self.assertEqual(number_to_hex(0), "0")


class LivePairTest(unittest.TestCase):
    def test_hex_matches_same_page_capture(self) -> None:
        seed = decode_seed(PAIR["seed"])
        hex_value = compute_hex(seed, PAIR["paths"])
        self.assertEqual(hex_value, PAIR["hex"])

    def test_seek_matches_capture(self) -> None:
        seed = decode_seed(PAIR["seed"])
        self.assertEqual(aligned_seek(seed, Formula()), 240)

    def test_sign_with_meta_wraps_70_byte_shell(self) -> None:
        now = 1757419200  # 2026-09-09 12:00:00 UTC
        value = sign_with_meta(
            "post",
            "/rest/app-chat/conversations/new",
            PAIR["seed"],
            now,
            PAIR["paths"],
        )
        self.assertTrue(valid_statsig_id(value))
        info = inspect_statsig_id(value)
        self.assertEqual(info["mark"], 3)
        self.assertEqual(info["now_unix"], now)
        expected = build_statsig(
            decode_seed(PAIR["seed"]),
            PAIR["hex"],
            "POST",
            "/rest/app-chat/conversations/new",
            now,
            key=info["key"],
        )
        self.assertEqual(value, expected)


if __name__ == "__main__":
    unittest.main()
