from __future__ import annotations

import json
import os
import sys
import time
from typing import Any
from uuid import uuid4

from .algorithm import curves_hash
from .capture import HOOK_JS, PROBE_JS, discover_signer_chunks, load_secrets
from .htmlutil import extract_curve_paths, extract_script_urls, extract_sentry_release


def capture_x2api(
    addr: str | None = None,
    url: str = "https://grok.com/imagine",
    headed: bool = False,
    timeout_ms: int = 45000,
) -> dict[str, Any]:
    stub = _stub(addr)
    secrets = load_secrets()
    sso = secrets.get("local16_sso") or secrets.get("grokx_sso") or ""
    cookies = []
    if sso:
        cookies = [
            {"name": "sso", "value": sso, "domain": "grok.com", "path": "/", "secure": True, "httpOnly": True},
            {"name": "sso-rw", "value": sso, "domain": "grok.com", "path": "/", "secure": True, "httpOnly": True},
        ]
    profile_id = f"x2api-agent-statsig-{uuid4().hex[:12]}"
    pb2 = _pb2()
    stub.CreateProfile(pb2.CreateProfileRequest(profile=pb2.Profile(profile_id=profile_id)), timeout=30)
    session = stub.CreateSession(
        pb2.CreateSessionRequest(
            profile_id=profile_id,
            headed=headed,
            persistent=True,
            idle_ttl_seconds=180,
            start_url="about:blank",
            initial_cookies_json=json.dumps(cookies),
            labels={"owner": "x2api-agent", "purpose": "statsig"},
        ),
        timeout=180,
    )
    session_id = session.session.session_id
    try:
        _execute(
            stub,
            session_id,
            pb2.Action(
                navigate=pb2.NavigateAction(url=url, wait_until="domcontentloaded", timeout_ms=timeout_ms)
            ),
        )
        _eval(stub, session_id, HOOK_JS)
        hooked: dict[str, Any] = {}
        deadline = time.time() + timeout_ms / 1000
        i = 0
        while time.time() < deadline:
            if i in (0, 4, 10, 16):
                _eval(stub, session_id, PROBE_JS)
            hooked = _eval(stub, session_id, "() => window.__sig") or {}
            digest = hooked.get("digest") or {}
            if digest.get("hex"):
                break
            time.sleep(0.4)
            i += 1
        html = _eval(stub, session_id, "() => document.documentElement.outerHTML") or ""
        if not isinstance(html, str):
            html = str(html)
        paths = extract_curve_paths(html)
        digest = hooked.get("digest") or {}
        return {
            "ok": bool(digest.get("seed") and digest.get("hex") and len(paths) == 4),
            "browser": "x2api",
            "url": url,
            "seed": digest.get("seed") or "",
            "hex": digest.get("hex") or "",
            "salt": digest.get("salt") or "",
            "prefix": digest.get("prefix") or "",
            "paths": paths,
            "seeks": hooked.get("seeks") or [],
            "anims": hooked.get("anims") or [],
            "chunks": [],
            "script_urls": extract_script_urls(html),
            "sentry_release": extract_sentry_release(html),
            "curves_hash": curves_hash(paths) if paths else "",
            "digest_raw": hooked.get("digestRaw") or "",
            "signer_chunks": discover_signer_chunks(extract_script_urls(html)),
        }
    finally:
        try:
            stub.CloseSession(pb2.CloseSessionRequest(session_id=session_id), timeout=90)
        except Exception:
            pass
        try:
            stub.DeleteProfile(pb2.DeleteProfileRequest(profile_id=profile_id), timeout=30)
        except Exception:
            pass


def _eval(stub: Any, session_id: str, expression: str) -> Any:
    pb2 = _pb2()
    result = _execute(
        stub,
        session_id,
        pb2.Action(evaluate=pb2.EvaluateAction(expression=expression, argument_json="null", timeout_ms=60000)),
    )
    if result.json_value:
        return json.loads(result.json_value)
    return result.text_value or None


def _execute(stub: Any, session_id: str, action: Any) -> Any:
    pb2 = _pb2()
    response = stub.Execute(pb2.ExecuteRequest(session_id=session_id, action=action), timeout=180)
    if not response.result.ok:
        raise RuntimeError(response.result.error or "x2api execute failed")
    return response.result


def _stub(addr: str | None) -> Any:
    try:
        import grpc
    except ImportError as exc:
        raise RuntimeError("x2api 抓包需要 grpcio，并指向 Browser Worker") from exc
    target = (addr or os.environ.get("X2API_BROWSER_ADDR") or "127.0.0.1:50051").strip()
    channel = grpc.insecure_channel(
        target,
        options=[
            ("grpc.max_receive_message_length", 32 * 1024 * 1024),
            ("grpc.max_send_message_length", 32 * 1024 * 1024),
        ],
    )
    return _pb2_grpc().BrowserServiceStub(channel)


def _ensure_generated() -> None:
    root = os.environ.get("X2API_ROOT", "/Users/real/Documents/code/x2api")
    src = os.path.join(root, "workers/browser/src")
    if src not in sys.path:
        sys.path.insert(0, src)


def _pb2() -> Any:
    _ensure_generated()
    from x2api_browser_worker.generated import browser_pb2 as pb2

    return pb2


def _pb2_grpc() -> Any:
    _ensure_generated()
    from x2api_browser_worker.generated import browser_pb2_grpc as pb2_grpc

    return pb2_grpc
