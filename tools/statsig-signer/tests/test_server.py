from __future__ import annotations

import json
import threading
import time
import unittest
import urllib.request
from pathlib import Path

from statsig_signer.algorithm import decode_seed, inspect_statsig_id, valid_statsig_id
from statsig_signer.server import serve
from statsig_signer.store import Store

ROOT = Path(__file__).resolve().parents[1]
PAIR = json.loads((ROOT / "data" / "pair.json").read_text(encoding="utf-8"))


class SignHTTPTest(unittest.TestCase):
    def setUp(self) -> None:
        self.server = serve("127.0.0.1", 0, Store(ROOT / "data"))
        self.port = int(self.server.server_address[1])
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self) -> None:
        self.server.shutdown()
        self.server.server_close()

    def test_sign_uses_request_meta_and_70_byte_shell(self) -> None:
        body = json.dumps(
            {
                "method": "POST",
                "path": "/rest/app-chat/conversations/new",
                "environment": {"metaContent": PAIR["seed"]},
            }
        ).encode("utf-8")
        request = urllib.request.Request(
            f"http://127.0.0.1:{self.port}/sign",
            data=body,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(request, timeout=5) as response:
            payload = json.loads(response.read().decode("utf-8"))
        value = payload["x-statsig-id"]
        self.assertTrue(valid_statsig_id(value))
        info = inspect_statsig_id(value)
        self.assertEqual(decode_seed(info["seed"]), decode_seed(PAIR["seed"]))
        self.assertEqual(info["mark"], 3)
        self.assertLessEqual(abs(info["now_unix"] - int(time.time())), 2)

    def test_health(self) -> None:
        with urllib.request.urlopen(f"http://127.0.0.1:{self.port}/health", timeout=5) as response:
            payload = json.loads(response.read().decode("utf-8"))
        self.assertTrue(payload["ok"])
        self.assertEqual(payload["formula"]["path_index"], 5)
        self.assertEqual(payload["path_count"], 4)

    def test_fingerprint_exposes_frontend_state(self) -> None:
        with urllib.request.urlopen(f"http://127.0.0.1:{self.port}/fingerprint", timeout=5) as response:
            payload = json.loads(response.read().decode("utf-8"))
        self.assertTrue(payload["ok"])
        self.assertEqual(payload["path_count"], 4)
        self.assertIn("frontend", payload)
