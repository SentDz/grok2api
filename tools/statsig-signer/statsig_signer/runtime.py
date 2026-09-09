from __future__ import annotations

import base64
import json
import os
import select
import shutil
import subprocess
import threading
from pathlib import Path
from typing import Any

from .algorithm import Formula, compute_hex as builtin_compute_hex, decode_seed

PACKAGE_DIR = Path(__file__).resolve().parent
HOT_DIR = PACKAGE_DIR.parent / "hot"
HEX_JS = HOT_DIR / "hex.js"
HEX_PY = HOT_DIR / "hex.py"
WORKER_JS = HOT_DIR / "eval_worker.js"
EVAL_TIMEOUT_SEC = 0.25
WORKER_START_TIMEOUT_SEC = 2.0


class HotRuntime:
    """Eval hot HEX code. Node worker stays alive; JS is compiled once per source change."""

    def __init__(self, hot_dir: Path | None = None) -> None:
        self.hot_dir = Path(hot_dir) if hot_dir else HOT_DIR
        self.js_path = self.hot_dir / "hex.js"
        self.py_path = self.hot_dir / "hex.py"
        self.worker_js = self.hot_dir / "eval_worker.js"
        self._mu = threading.Lock()
        self._proc: subprocess.Popen[str] | None = None
        self._js_mtime = 0.0
        self._js_source = ""
        self._py_mtime = 0.0
        self._py_fn: Any = None

    def close(self) -> None:
        with self._mu:
            self._stop_locked()

    def current_js(self) -> str:
        path = self.js_path if self.js_path.exists() else HEX_JS
        if not path.exists():
            return ""
        mtime = path.stat().st_mtime
        with self._mu:
            if self._js_source and mtime == self._js_mtime:
                return self._js_source
        text = path.read_text(encoding="utf-8")
        with self._mu:
            self._js_mtime = mtime
            self._js_source = text
        return text

    def compute(self, seed: bytes, paths: list[str], formula: Formula | None = None) -> str:
        source = self.current_js()
        if source.strip():
            try:
                return self.eval_js(source, seed, paths)
            except Exception:
                pass
        py_hex = self._eval_py_file(seed, paths)
        if py_hex:
            return py_hex
        return builtin_compute_hex(seed, paths, formula)

    def eval_js(self, source: str, seed: bytes, paths: list[str]) -> str:
        source = source.strip()
        if not source:
            raise ValueError("hot JS 为空")
        payload = json.dumps(
            {
                "source": source,
                "seed": base64.b64encode(seed).decode("ascii"),
                "paths": list(paths),
            },
            ensure_ascii=False,
        )
        with self._mu:
            starting = self._proc is None or self._proc.poll() is not None
            proc = self._ensure_worker_locked()
            try:
                proc.stdin.write(payload + "\n")
                proc.stdin.flush()
            except Exception:
                self._stop_locked()
                raise
            # Container cold starts need more time than a warm JS evaluation.
            timeout = WORKER_START_TIMEOUT_SEC if starting else EVAL_TIMEOUT_SEC
            line = self._readline_locked(proc, timeout)
            if line is None:
                self._stop_locked()
                raise RuntimeError("JS eval timeout")
        if not line:
            self.close()
            raise RuntimeError("JS eval worker 已退出")
        data = json.loads(line)
        if not data.get("ok"):
            raise RuntimeError(data.get("error") or "JS eval 失败")
        hex_value = str(data.get("hex") or "").strip()
        if not hex_value:
            raise RuntimeError("JS eval 没有返回 HEX")
        return hex_value

    def write_js(self, source: str) -> Path:
        self.hot_dir.mkdir(parents=True, exist_ok=True)
        self.js_path.write_text(source.strip() + "\n", encoding="utf-8")
        return self.js_path

    def _eval_py_file(self, seed: bytes, paths: list[str]) -> str:
        path = self.py_path
        if not path.exists():
            return ""
        mtime = path.stat().st_mtime
        with self._mu:
            if self._py_fn is None or mtime != self._py_mtime:
                ns: dict[str, Any] = {}
                exec(compile(path.read_text(encoding="utf-8"), str(path), "exec"), ns, ns)
                fn = ns.get("compute_hex") or ns.get("computeHex")
                if not callable(fn):
                    raise RuntimeError("hot/hex.py 必须定义 compute_hex(seed, paths)")
                self._py_fn = fn
                self._py_mtime = mtime
            fn = self._py_fn
        return str(fn(seed, list(paths))).strip()

    def _readline_locked(self, proc: subprocess.Popen[str], timeout: float) -> str | None:
        stdout = proc.stdout
        if stdout is None:
            return ""
        ready, _, _ = select.select([stdout], [], [], timeout)
        if not ready:
            return None
        return stdout.readline()

    def _ensure_worker_locked(self) -> subprocess.Popen[str]:
        proc = self._proc
        if proc is not None and proc.poll() is None:
            return proc
        node = os.environ.get("STATSIG_NODE", "").strip() or shutil.which("node")
        if not node:
            raise RuntimeError("签名器 JS eval 需要 node")
        worker = self.worker_js if self.worker_js.exists() else WORKER_JS
        proc = subprocess.Popen(
            [node, str(worker)],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            bufsize=1,
        )
        self._proc = proc
        return proc

    def _stop_locked(self) -> None:
        proc = self._proc
        self._proc = None
        if proc is None:
            return
        try:
            if proc.stdin:
                proc.stdin.close()
        except Exception:
            pass
        try:
            proc.kill()
        except Exception:
            pass
        try:
            proc.wait(timeout=1)
        except Exception:
            pass


_runtime: HotRuntime | None = None
_runtime_mu = threading.Lock()


def default_runtime() -> HotRuntime:
    global _runtime
    with _runtime_mu:
        if _runtime is None:
            _runtime = HotRuntime()
        return _runtime


def compute_hex_hot(seed: bytes, paths: list[str], formula: Formula | None = None) -> str:
    return default_runtime().compute(seed, paths, formula)


def sign_with_meta_hot(method: str, path: str, meta_content: str, now_unix: int, paths: list[str], formula: Formula | None = None) -> str:
    from .algorithm import build_statsig

    seed = decode_seed(meta_content)
    hex_value = compute_hex_hot(seed, list(paths), formula)
    return build_statsig(seed, hex_value, method, path, now_unix, formula)
