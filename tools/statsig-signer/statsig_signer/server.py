from __future__ import annotations

import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import urlparse

from .algorithm import valid_statsig_id
from .runtime import sign_with_meta_hot
from .store import Store
from .deployment import material, sign_published, state_dir


class SignerHandler(BaseHTTPRequestHandler):
    store: Store
    protocol_version = "HTTP/1.1"

    def log_message(self, format: str, *args: Any) -> None:
        sys_stderr_write = super().log_message
        sys_stderr_write(format, *args)

    def do_GET(self) -> None:
        path = urlparse(self.path).path
        if path in ("/health", "/", "/fingerprint"):
            snapshot = material()
            pair = None
            try:
                pair = self.store.pair
            except FileNotFoundError:
                pass
            if snapshot:
                from .store import Pair

                pair = Pair.from_dict(snapshot["pair"])
            body = {
                "ok": True,
                "formula": snapshot["formula"] if snapshot else self.store.formula.to_dict(),
                "path_count": len(pair.paths) if pair else 0,
                "hex_len": len(pair.hex) if pair else 0,
                "curves_hash": pair.curves_hash if pair else "",
                "updated_at": pair.updated_at if pair else "",
                "verified": snapshot.get("verified", False) if snapshot else None,
                "published_at": snapshot.get("published_at") if snapshot else None,
            }
            if path == "/fingerprint":
                from .watch import load_previous

                directory = state_dir()
                body["frontend"] = load_previous(Store(directory / "watch") if directory else self.store)
                if directory and (directory / "watch_status.json").exists():
                    body["watch"] = json.loads((directory / "watch_status.json").read_text(encoding="utf-8"))
            self._json(200, body)
            return
        self._json(404, {"error": "not found"})

    def do_POST(self) -> None:
        path = urlparse(self.path).path
        if path == "/reload":
            self.store.reload(force=True)
            self._json(200, {"ok": True})
            return
        if path != "/sign":
            self._json(404, {"error": "not found"})
            return
        try:
            payload = self._read_json()
            method = str(payload.get("method") or "").strip()
            target = str(payload.get("path") or "").strip()
            env = payload.get("environment") if isinstance(payload.get("environment"), dict) else {}
            meta = str(env.get("metaContent") or payload.get("metaContent") or "").strip()
            if not method or not target or not meta:
                self._json(400, {"error": "method、path、environment.metaContent 必填"})
                return
            snapshot = material()
            if snapshot:
                value = sign_published(snapshot, method, target, meta, _now_unix())
            else:
                pair = self.store.pair
                value = sign_with_meta_hot(method, target, meta, _now_unix(), pair.paths, self.store.formula)
            if not valid_statsig_id(value):
                self._json(500, {"error": "签名结果无效"})
                return
            self._json(200, {"x-statsig-id": value})
        except FileNotFoundError as exc:
            self._json(500, {"error": str(exc)})
        except ValueError as exc:
            self._json(400, {"error": str(exc)})
        except Exception as exc:
            self._json(500, {"error": f"签名失败: {exc}"})

    def _read_json(self) -> dict[str, Any]:
        length = int(self.headers.get("Content-Length") or 0)
        if length <= 0 or length > 1_048_576:
            raise ValueError("请求体无效")
        raw = self.rfile.read(length)
        data = json.loads(raw.decode("utf-8"))
        if not isinstance(data, dict):
            raise ValueError("请求必须是 JSON 对象")
        return data

    def _json(self, status: int, payload: dict[str, Any]) -> None:
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(body)


def _now_unix() -> int:
    import time

    return int(time.time())


def serve(host: str = "127.0.0.1", port: int = 8788, store: Store | None = None) -> ThreadingHTTPServer:
    handler = SignerHandler
    handler.store = store or Store()
    server = ThreadingHTTPServer((host, port), handler)
    return server
