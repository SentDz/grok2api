from __future__ import annotations

import json
import os
import threading
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from .algorithm import curves_hash, decode_seed
from .capture import capture, load_secrets
from .htmlutil import chunks_hash, extract_curve_paths, extract_script_urls, extract_sentry_release
from .runtime import default_runtime
from .store import Store, atomic_write_json

PROBE_URL = "https://grok.com/imagine"


def fingerprint_path(store: Store) -> Path:
    return store.directory / "frontend_fingerprint.json"


def fingerprint_from_html(html: str) -> dict[str, Any]:
    paths = extract_curve_paths(html)
    scripts = extract_script_urls(html)
    return {
        "sentry_release": extract_sentry_release(html),
        "curves_hash": curves_hash(paths) if paths else "",
        "chunks_hash": chunks_hash(scripts),
        "path_count": len(paths),
        "script_count": len(scripts),
        "source": "html",
    }


def fingerprint_from_capture(captured: dict[str, Any]) -> dict[str, Any]:
    paths = list(captured.get("paths") or [])
    scripts = list(captured.get("script_urls") or captured.get("chunks") or [])
    return {
        "sentry_release": captured.get("sentry_release") or "",
        "curves_hash": captured.get("curves_hash") or (curves_hash(paths) if paths else ""),
        "chunks_hash": chunks_hash(scripts),
        "path_count": len(paths),
        "official_hex": captured.get("hex") or "",
        "hex_len": len(captured.get("hex") or ""),
        "salt": captured.get("salt") or "",
        "seek": ((captured.get("seeks") or [{}])[0] or {}).get("value"),
        "source": "capture",
        "ok": bool(captured.get("ok")),
    }


def should_backoff_repair(previous: dict[str, Any] | None, changed: bool) -> bool:
    if not previous or changed or previous.get("repair_ok") is not False:
        return False
    retry = max(30, int(os.environ.get("STATSIG_REPAIR_RETRY_SECONDS", "300")))
    return time.time() - previous.get("repair_attempted_at", 0) < retry


def compare_fingerprints(previous: dict[str, Any] | None, current: dict[str, Any]) -> list[str]:
    """Compare deploy markers that exist in HTML. Ignore last-12 filenames and signer_urls.

    grok.com HTML already has sentry-release (git SHA), escaped curves, and content-hashed
    chunk names. Index-only JS changes still rename chunks. HTML vs Playwright fingerprints
    must not compare different URL lists or every deep tick looks like a deploy.
    """
    if not previous:
        return ["first_seen"]
    changes: list[str] = []
    for key in ("sentry_release", "curves_hash"):
        if previous.get(key) and current.get(key) and previous.get(key) != current.get(key):
            changes.append(key)
    if previous.get("chunks_hash") and current.get("chunks_hash") and previous.get("source") == current.get("source"):
        if previous.get("chunks_hash") != current.get("chunks_hash"):
            changes.append("chunks")
    return changes


def probe_html() -> tuple[dict[str, Any] | None, str]:
    secrets = load_secrets()
    sso = secrets.get("local16_sso") or secrets.get("grokx_sso") or secrets.get("GROK_SSO") or ""
    request = urllib.request.Request(
        PROBE_URL,
        headers={
            "Accept": "text/html,application/xhtml+xml",
            "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36",
        },
    )
    if sso:
        request.add_header("Cookie", f"sso={sso}; sso-rw={sso}")
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            html = response.read()[:2_000_000].decode("utf-8", errors="replace")
            status = response.status
    except urllib.error.HTTPError as exc:
        return None, f"html HTTP {exc.code}"
    except Exception as exc:
        return None, f"html {exc}"
    if "Just a moment" in html or "cf-browser-verification" in html:
        return None, "cloudflare_challenge"
    if status >= 400:
        return None, f"html HTTP {status}"
    fp = fingerprint_from_html(html)
    if not fp.get("curves_hash") and not fp.get("sentry_release"):
        return None, "html_missing_markers"
    return fp, ""


def check_hex_match(captured: dict[str, Any]) -> dict[str, Any]:
    seed = captured.get("seed") or ""
    hex_value = captured.get("hex") or ""
    paths = list(captured.get("paths") or [])
    if not seed or not hex_value or len(paths) < 4:
        return {"ok": False, "error": "capture incomplete"}
    try:
        from .deployment import material

        snapshot = material()
        if snapshot:
            got = default_runtime().eval_js(snapshot["js"], decode_seed(seed), paths)
        else:
            got = default_runtime().compute(decode_seed(seed), paths)
    except Exception as exc:
        return {"ok": False, "error": str(exc)}
    return {"ok": got == hex_value, "official": hex_value, "computed": got}


def probe(deep: bool = False, browser: str = "local", **capture_kwargs: Any) -> dict[str, Any]:
    observed_at = datetime.now(timezone.utc).isoformat()
    html_fp, html_error = (None, "skipped") if deep else probe_html()
    captured = None
    if deep or html_fp is None:
        try:
            captured = capture(browser=browser, **capture_kwargs)
        except Exception as exc:
            return {
                "ok": False,
                "changed": False,
                "error": f"capture failed: {exc}",
                "html_error": html_error,
                "observed_at": observed_at,
            }
        fp = fingerprint_from_capture(captured)
        hex_check = check_hex_match(captured)
    else:
        fp = html_fp
        hex_check = {"ok": None, "skipped": True}
    fp["observed_at"] = observed_at
    return {
        "ok": True,
        "fingerprint": fp,
        "html_error": html_error,
        "hex_match": hex_check,
        "observed_at": observed_at,
    }


def load_previous(store: Store) -> dict[str, Any] | None:
    path = fingerprint_path(store)
    if not path.exists():
        return None
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError:
        return None


def save_fingerprint(store: Store, fingerprint: dict[str, Any]) -> None:
    atomic_write_json(fingerprint_path(store), fingerprint)


def tick(
    store: Store | None = None,
    deep: bool = False,
    repair: bool = False,
    browser: str = "local",
    **capture_kwargs: Any,
) -> dict[str, Any]:
    store = store or Store()
    previous = load_previous(store)
    probed = probe(deep=deep, browser=browser, **capture_kwargs)
    if not probed.get("ok"):
        return probed
    current = probed["fingerprint"]
    changes = compare_fingerprints(previous, current)
    fingerprint_changed = bool(changes) and changes != ["first_seen"]
    hex_match = probed.get("hex_match") or {}
    if hex_match.get("ok") is False and not hex_match.get("skipped"):
        if "hex_mismatch" not in changes:
            changes.append("hex_mismatch")
    changed = bool(changes) and changes != ["first_seen"]
    report = {
        "ok": True,
        "changed": changed,
        "changes": changes,
        "hex_match": hex_match,
        "fingerprint": current,
        "previous": previous,
        "observed_at": probed["observed_at"],
    }
    from .deployment import material, repair_published, state_dir

    snapshot = material() if state_dir() else None
    pending = bool(previous) and previous.get("repair_ok") is False
    unverified = snapshot is not None and not snapshot.get("verified")
    need_repair = repair and (not previous or unverified or pending or changed or hex_match.get("ok") is False)
    if need_repair and should_backoff_repair(previous, fingerprint_changed):
        report["ok"] = False
        report["repair"] = {"ok": False, "skipped": "backoff"}
        return report
    if need_repair:
        from .agent import update

        try:
            if state_dir():
                report["repair"] = repair_published(browser=browser, **capture_kwargs)
            else:
                report["repair"] = update(store=store, browser=browser, **capture_kwargs)
        except Exception as exc:
            report["repair"] = {"ok": False, "error": str(exc)}
        current = dict(current)
        current["repair_ok"] = bool(report["repair"].get("ok"))
        current["repair_attempted_at"] = time.time()
        report["ok"] = current["repair_ok"]
        report["fingerprint"] = current
        save_fingerprint(store, current)
        return report
    if not previous or not changed:
        if previous:
            for key in ("repair_ok", "repair_attempted_at"):
                if key in previous:
                    current[key] = previous[key]
        save_fingerprint(store, current)
    return report


def watch_loop(interval: int = 60, repair: bool = False, deep_every: int = 0, browser: str = "local") -> None:
    from .deployment import initialize_state

    directory = initialize_state()
    if directory and repair:
        missing = [name for name in ("GROK2API_BASE", "GROK2API_KEY", "GROK2API_MODEL", "GROK_SSO") if not os.environ.get(name, "").strip()]
        if missing:
            raise RuntimeError("Missing watcher configuration: " + ", ".join(missing))
    store = Store(directory / "watch") if directory else Store()
    status: dict[str, Any] = {"started_at": time.time(), "state": "starting", "repair_enabled": repair}
    lock = threading.Lock()
    stopped = threading.Event()

    def heartbeat() -> None:
        while not stopped.is_set():
            if directory:
                with lock:
                    status["heartbeat_at"] = time.time()
                    atomic_write_json(directory / "watch_status.json", dict(status))
            stopped.wait(10)

    worker = threading.Thread(target=heartbeat, daemon=True)
    worker.start()
    n = 0
    try:
        while True:
            with lock:
                status["state"] = "checking"
            try:
                deep = deep_every > 0 and n % deep_every == 0
                report = tick(store=store, deep=deep, repair=repair, browser=browser)
            except Exception as exc:
                report = {"ok": False, "error": str(exc)}
            summary = {k: report[k] for k in ("ok", "changed", "changes", "hex_match", "observed_at", "error", "repair") if k in report}
            with lock:
                status.update(state="waiting", last_check_at=time.time(), last_result=summary)
            print(json.dumps(summary, ensure_ascii=False, default=str), flush=True)
            n += 1
            time.sleep(max(interval, 30))
    finally:
        stopped.set()
        worker.join(timeout=2)


def watch_health() -> bool:
    from .deployment import state_dir

    directory = state_dir()
    if directory is None:
        return False
    try:
        status = json.loads((directory / "watch_status.json").read_text(encoding="utf-8"))
        return time.time() - status["heartbeat_at"] < 90
    except (OSError, ValueError, KeyError):
        return False
