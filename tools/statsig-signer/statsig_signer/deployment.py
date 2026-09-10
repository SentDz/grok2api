from __future__ import annotations

import json
import os
import shutil
import time
from pathlib import Path
from tempfile import TemporaryDirectory
from typing import Any

from .algorithm import Formula, build_statsig, decode_seed
from .runtime import HOT_DIR, HotRuntime, default_runtime
from .store import DEFAULT_DATA_DIR, Pair, Store, atomic_write_json


def state_dir() -> Path | None:
    value = os.environ.get("STATSIG_STATE_DIR", "").strip()
    return Path(value) if value else None


def initialize_state() -> Path | None:
    directory = state_dir()
    if directory is None:
        return None
    directory.mkdir(parents=True, exist_ok=True)
    active = directory / "active.json"
    if not active.exists():
        bundled = Store(DEFAULT_DATA_DIR)
        atomic_write_json(active, {
            "pair": bundled.pair.to_dict(),
            "formula": bundled.formula.to_dict(),
            "js": (HOT_DIR / "hex.js").read_text(encoding="utf-8"),
            "published_at": None,
            "verified": False,
        })
    return directory


def material() -> dict[str, Any] | None:
    directory = initialize_state()
    if directory is None:
        return None
    return json.loads((directory / "active.json").read_text(encoding="utf-8"))


def sign_published(snapshot: dict[str, Any], method: str, path: str, meta: str, now: int) -> str:
    pair = Pair.from_dict(snapshot["pair"])
    formula = Formula.from_dict(snapshot["formula"])
    seed = decode_seed(meta)
    hex_value = default_runtime().eval_js(snapshot["js"], seed, pair.paths)
    return build_statsig(seed, hex_value, method, path, now, formula)


def repair_published(browser: str = "local", **kwargs: Any) -> dict[str, Any]:
    from .agent import update

    directory = initialize_state()
    assert directory is not None
    snapshot = material()
    assert snapshot is not None
    # Candidate writes remain private until the complete repair is verified.
    with TemporaryDirectory(prefix="repair-", dir=directory) as temporary:
        staging = Path(temporary)
        hot = staging / "hot"
        hot.mkdir()
        (hot / "hex.js").write_text(snapshot["js"], encoding="utf-8")
        shutil.copy(HOT_DIR / "eval_worker.js", hot / "eval_worker.js")
        store = Store(staging / "data")
        store.save_pair(Pair.from_dict(snapshot["pair"]))
        store.save_formula(Formula.from_dict(snapshot["formula"]))
        runtime = HotRuntime(hot)
        try:
            result = update(store=store, runtime=runtime, browser=browser, **kwargs)
            verified = bool(result.get("ok")) and (
                result.get("stage") == "matched" or result.get("stopped") == "exit"
            )
            if verified:
                atomic_write_json(directory / "active.json", {
                    "pair": store.pair.to_dict(),
                    "formula": store.formula.to_dict(),
                    "js": runtime.current_js(),
                    "published_at": time.time(),
                    "verified": True,
                })
            report = {"ok": verified, "stage": result.get("stage"), "published": verified, "stopped": result.get("stopped")}
            if not verified:
                report["error"] = str(result.get("reason") or result.get("content") or "Capture or verification failed")[:500]
            return report
        finally:
            runtime.close()
