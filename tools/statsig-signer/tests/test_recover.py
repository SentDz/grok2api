from __future__ import annotations

import json
import unittest
from pathlib import Path

from statsig_signer.algorithm import Formula, decode_seed
from statsig_signer.recover import recover_formula

ROOT = Path(__file__).resolve().parents[1]
PAIR = json.loads((ROOT / "data" / "pair.json").read_text(encoding="utf-8"))


class RecoverTest(unittest.TestCase):
    def test_current_formula_matches_live_pair(self) -> None:
        seed = decode_seed(PAIR["seed"])
        result = recover_formula(seed, PAIR["paths"], PAIR["hex"], Formula(), seek_hint=240)
        self.assertEqual(result.status, "matched")
        self.assertEqual(result.formula, Formula())

    def test_recovers_indices_from_wrong_formula(self) -> None:
        seed = decode_seed(PAIR["seed"])
        wrong = Formula(path_index=0, seg_index=1, seek_indices=(0, 1, 2))
        result = recover_formula(seed, PAIR["paths"], PAIR["hex"], wrong, seek_hint=240)
        self.assertEqual(result.status, "recovered")
        self.assertIsNotNone(result.formula)
        self.assertEqual(result.hex, PAIR["hex"])
        self.assertEqual(result.formula.path_index, 5)
        self.assertEqual(result.formula.seg_index, 39)
        self.assertEqual(tuple(result.formula.seek_indices), (3, 31, 36))
