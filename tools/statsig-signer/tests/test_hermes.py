from __future__ import annotations

import json
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory

from statsig_signer.algorithm import Formula, compute_hex, decode_seed
from statsig_signer.hermes import HermesKernel, Tool, _summarize_payload
from statsig_signer.recover import recover_formula
from statsig_signer.store import Pair, Store

ROOT = Path(__file__).resolve().parents[1]
PAIR = json.loads((ROOT / "data" / "pair.json").read_text(encoding="utf-8"))


class HermesKernelTest(unittest.TestCase):
    def test_exit_tool_stops_before_max_turns(self) -> None:
        calls = {"n": 0}

        def ping() -> dict:
            return {"ok": True}

        def exit_repair(reason: str = "") -> dict:
            return {"ok": True, "exit": True, "reason": reason or "done"}

        def complete(messages, tools):
            names = [item["function"]["name"] for item in tools]
            self.assertIn("exit", names)
            calls["n"] += 1
            if calls["n"] == 1:
                return {
                    "role": "assistant",
                    "tool_calls": [
                        {"id": "1", "type": "function", "function": {"name": "ping", "arguments": "{}"}},
                    ],
                }
            if calls["n"] == 2:
                return {
                    "role": "assistant",
                    "tool_calls": [
                        {
                            "id": "2",
                            "type": "function",
                            "function": {"name": "exit", "arguments": '{"reason":"verified"}'},
                        }
                    ],
                }
            self.fail("kernel continued after exit")
            return {"role": "assistant", "content": "should not run"}

        kernel = HermesKernel(
            complete,
            [
                Tool("ping", "", {"type": "object", "properties": {}}, ping),
                Tool("exit", "", {"type": "object", "properties": {"reason": {"type": "string"}}}, exit_repair),
            ],
            max_turns=20,
        )
        result = kernel.run("sys", "fix")
        self.assertEqual(result.stopped, "exit")
        self.assertEqual(result.turns, 2)
        self.assertEqual(result.content, "verified")
        self.assertEqual(calls["n"], 2)

    def test_verify_payload_exit_stops_loop(self) -> None:
        calls = {"n": 0}

        def verify() -> dict:
            return {"ok": True, "exit": True, "reason": "verify_signature 通过"}

        def complete(messages, tools):
            calls["n"] += 1
            if calls["n"] == 1:
                return {
                    "role": "assistant",
                    "tool_calls": [
                        {"id": "1", "type": "function", "function": {"name": "verify_signature", "arguments": "{}"}},
                    ],
                }
            self.fail("kernel continued after verify exit")
            return {"role": "assistant", "content": "nope"}

        kernel = HermesKernel(
            complete,
            [Tool("verify_signature", "", {"type": "object", "properties": {}}, verify)],
            max_turns=20,
        )
        result = kernel.run("sys", "fix")
        self.assertEqual(result.stopped, "exit")
        self.assertEqual(result.turns, 1)
        self.assertEqual(calls["n"], 1)

    def test_summarize_payload_keeps_next_and_hex_js_len(self) -> None:
        summary = json.loads(
            _summarize_payload(
                {
                    "ok": False,
                    "error": "第二页官方 HEX 不匹配，拒绝写入单次反解",
                    "next": "write_hot_js",
                    "hex_js": "x" * 80,
                    "second": {"url": "https://grok.com/", "hex": "aa", "official": "bb"},
                }
            )
        )
        self.assertEqual(summary["next"], "write_hot_js")
        self.assertEqual(summary["hex_js_len"], 80)
        self.assertFalse(summary["second_match"])

    def test_tool_loop_applies_recovered_formula(self) -> None:
        with TemporaryDirectory() as tmp:
            data_dir = Path(tmp)
            (data_dir / "formula.json").write_text(
                json.dumps(Formula(path_index=0, seg_index=1, seek_indices=[0, 1, 2]).to_dict()),
                encoding="utf-8",
            )
            (data_dir / "pair.json").write_text(json.dumps(PAIR), encoding="utf-8")
            store = Store(data_dir)
            seed = decode_seed(PAIR["seed"])

            def recover() -> dict:
                result = recover_formula(seed, PAIR["paths"], PAIR["hex"], store.formula, 240)
                payload = {"status": result.status, "reason": result.reason}
                if result.formula:
                    payload["formula"] = result.formula.to_dict()
                return payload

            def apply_formula(formula: dict) -> dict:
                next_formula = Formula.from_dict(formula)
                hex_value = compute_hex(seed, PAIR["paths"], next_formula)
                if hex_value != PAIR["hex"]:
                    return {"ok": False, "error": "hex mismatch"}
                store.save_formula(next_formula)
                store.save_pair(Pair.from_dict(PAIR))
                return {"ok": True}

            calls = {"n": 0}

            def complete(messages, tools):
                calls["n"] += 1
                if calls["n"] == 1:
                    return {
                        "role": "assistant",
                        "content": None,
                        "tool_calls": [
                            {
                                "id": "1",
                                "type": "function",
                                "function": {"name": "recover_indices", "arguments": "{}"},
                            }
                        ],
                    }
                if calls["n"] == 2:
                    formula = json.loads(messages[-1]["content"])["formula"]
                    return {
                        "role": "assistant",
                        "content": None,
                        "tool_calls": [
                            {
                                "id": "2",
                                "type": "function",
                                "function": {
                                    "name": "apply_formula",
                                    "arguments": json.dumps({"formula": formula}),
                                },
                            }
                        ],
                    }
                return {"role": "assistant", "content": "updated"}

            kernel = HermesKernel(
                complete,
                [
                    Tool("recover_indices", "", {"type": "object", "properties": {}}, recover),
                    Tool(
                        "apply_formula",
                        "",
                        {"type": "object", "properties": {"formula": {"type": "object"}}, "required": ["formula"]},
                        apply_formula,
                    ),
                ],
            )
            result = kernel.run("sys", "fix formula")
            self.assertEqual(result.content, "updated")
            self.assertEqual(result.turns, 3)
            self.assertEqual(store.formula.path_index, 5)
            self.assertEqual(store.formula.seg_index, 39)
            self.assertEqual(tuple(store.formula.seek_indices), (3, 31, 36))
