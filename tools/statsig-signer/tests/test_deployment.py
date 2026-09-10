from __future__ import annotations

import json
import os
import time
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch

from statsig_signer.algorithm import build_statsig, decode_seed, inspect_statsig_id
from statsig_signer.deployment import initialize_state, material, repair_published, sign_published
from statsig_signer.runtime import default_runtime
from statsig_signer.store import Store, atomic_write_json
from statsig_signer.watch import save_fingerprint, tick, watch_health


class PublishedMaterialTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        env = patch.dict(os.environ, {"STATSIG_STATE_DIR": self.temporary.name})
        env.start()
        self.addCleanup(env.stop)
        self.addCleanup(default_runtime().close)
        initialize_state()

    def test_restart_preserves_verified_material(self) -> None:
        snapshot = material()
        snapshot.update(verified=True, published_at=123)
        atomic_write_json(self.directory / "active.json", snapshot)
        initialize_state()
        self.assertEqual(material(), snapshot)

    def test_unverified_candidate_never_changes_active_material(self) -> None:
        original = material()

        def fake_update(**kwargs):
            kwargs["runtime"].write_js("function computeHex() { return 'bad'; }")
            self.assertEqual(material(), original)
            return {"ok": True, "stage": "hermes", "stopped": "max_turns"}

        with patch("statsig_signer.agent.update", fake_update):
            result = repair_published()
        self.assertFalse(result["published"])
        self.assertEqual(material(), original)
        self.assertEqual(list(self.directory.glob("repair-*")), [])

    def test_verified_candidate_is_published_and_loaded_without_restart(self) -> None:
        original = material()

        def fake_update(**kwargs):
            kwargs["runtime"].write_js(original["js"] + "\n// verified candidate")
            return {"ok": True, "stage": "hermes", "stopped": "exit"}

        with patch("statsig_signer.agent.update", fake_update):
            self.assertTrue(repair_published()["published"])
        snapshot = material()
        self.assertTrue(snapshot["verified"])
        self.assertIn("verified candidate", snapshot["js"])
        for hex_value in ("aa", "bb"):
            snapshot["js"] = "function computeHex() { return '" + hex_value + "'; }"
            atomic_write_json(self.directory / "active.json", snapshot)
            now = int(time.time())
            signed = sign_published(material(), "POST", "/test", snapshot["pair"]["seed"], now)
            info = inspect_statsig_id(signed)
            expected = build_statsig(decode_seed(snapshot["pair"]["seed"]), hex_value, "POST", "/test", now, key=info["key"])
            self.assertEqual(signed, expected)

    def test_failed_repair_retries_unchanged_fingerprint_after_backoff(self) -> None:
        store = Store(self.directory / "watch")
        fingerprint = {"source": "html", "curves_hash": "same", "sentry_release": "same"}
        probe = {"ok": True, "fingerprint": fingerprint, "hex_match": {"ok": None}, "observed_at": "test"}
        with patch("statsig_signer.watch.probe", return_value=probe), patch(
            "statsig_signer.deployment.repair_published", side_effect=RuntimeError("temporary outage")
        ) as repair:
            self.assertFalse(tick(store=store, repair=True)["ok"])
            self.assertEqual(repair.call_count, 1)
            self.assertEqual(tick(store=store, repair=True)["repair"]["skipped"], "backoff")
            self.assertEqual(repair.call_count, 1)
            probe["hex_match"] = {"ok": False}
            self.assertEqual(tick(store=store, repair=True)["repair"]["skipped"], "backoff")
            self.assertEqual(repair.call_count, 1)
            previous = json.loads((store.directory / "frontend_fingerprint.json").read_text())
            previous["repair_attempted_at"] = 0
            save_fingerprint(store, previous)
            tick(store=store, repair=True)
            self.assertEqual(repair.call_count, 2)

    def test_watcher_health_requires_recent_heartbeat(self) -> None:
        self.assertFalse(watch_health())
        atomic_write_json(self.directory / "watch_status.json", {"heartbeat_at": time.time()})
        self.assertTrue(watch_health())
        atomic_write_json(self.directory / "watch_status.json", {"heartbeat_at": time.time() - 100})
        self.assertFalse(watch_health())
