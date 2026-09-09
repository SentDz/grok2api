from __future__ import annotations

import json
import unittest
from pathlib import Path

from statsig_signer.algorithm import decode_seed
from statsig_signer.runtime import HotRuntime

ROOT = Path(__file__).resolve().parents[1]
PAIR = json.loads((ROOT / "data" / "pair.json").read_text(encoding="utf-8"))


class HotEvalTest(unittest.TestCase):
    def test_js_eval_matches_live_pair(self) -> None:
        runtime = HotRuntime(ROOT / "hot")
        self.addCleanup(runtime.close)
        seed = decode_seed(PAIR["seed"])
        hex_value = runtime.compute(seed, PAIR["paths"])
        self.assertEqual(hex_value, PAIR["hex"])

    def test_eval_timeout_recovers(self) -> None:
        import time

        runtime = HotRuntime(ROOT / "hot")
        self.addCleanup(runtime.close)
        source = "function computeHex(seed, paths) { while (true) {} }"
        start = time.time()
        with self.assertRaises(RuntimeError):
            runtime.eval_js(source, b"\x00" * 48, ["M 0 0"] * 4)
        self.assertLess(time.time() - start, 2)
        seed = decode_seed(PAIR["seed"])
        self.assertEqual(runtime.compute(seed, PAIR["paths"]), PAIR["hex"])

    def test_eval_rejects_wrong_source(self) -> None:
        runtime = HotRuntime(ROOT / "hot")
        self.addCleanup(runtime.close)
        seed = decode_seed(PAIR["seed"])
        source = runtime.current_js().replace("seed[5] % 4", "seed[0] % 4", 1)
        hex_value = runtime.eval_js(source, seed, PAIR["paths"])
        self.assertNotEqual(hex_value, PAIR["hex"])
