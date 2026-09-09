from __future__ import annotations

import json
import os
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from .algorithm import Formula, build_statsig, compute_hex, curves_hash, decode_seed, inspect_statsig_id
from .capture import capture
from .hermes import HermesKernel, Tool, grok2api_complete
from .recover import recover_formula
from .runtime import HotRuntime, default_runtime
from .store import Pair, Store

SYSTEM = """你是 grok.com x-statsig-id 维修内核。
算法是可执行 JS：签名器常驻 Node，用 vm.eval 跑 hot/hex.js 里的 computeHex(seed, paths)。
不要改 Go。需要新鲜对照时自己调用 capture_page（本机浏览器或 x2api），钩 digest 和 animate(4096)。
70 字节壳仍由签名器 Python 套（epoch 1682924400，salt obfiowerehiring，末字节 0x03）。
步骤：capture_page → inspect_capture / find_signer_chunk → recover_indices；
对不上就按 chunk 源码改 computeHex。eval_hot_js 对上官方 HEX 才能 write_hot_js。
verify_signature 通过才接受。前端大改时反复抓包、拉 chunk、eval，最多 200 轮。
对不上官方 HEX 不准写入。
"""


def update(
    store: Store | None = None,
    browser: str = "local",
    use_hermes: bool = True,
    force_hermes: bool = False,
    fixture: dict[str, Any] | None = None,
    runtime: HotRuntime | None = None,
    defer_capture: bool = False,
    capture_fn: Any = None,
    **capture_kwargs: Any,
) -> dict[str, Any]:
    store = store or Store()
    runtime = runtime or default_runtime()
    do_capture = capture_fn or capture
    if defer_capture and fixture is None:
        captured = {"ok": False, "browser": browser, "paths": [], "script_urls": [], "chunks": []}
    else:
        captured = fixture or do_capture(browser=browser, **capture_kwargs)
        if not captured.get("ok") and not (captured.get("seed") and captured.get("hex") and captured.get("paths")):
            return {"ok": False, "stage": "capture", "capture": _public_capture(captured)}
        captured["ok"] = True
    if not (captured.get("seed") and captured.get("hex") and captured.get("paths")):
        kernel = _kernel(store, captured, runtime, do_capture, capture_kwargs)
        prompt = json.dumps(
            {
                "task": "先 capture_page 抓同一页 seed/官方 HEX/curves，再修复 computeHex，verify_signature 通过才结束。",
                "current_formula": store.formula.to_dict(),
                "hot_js": runtime.current_js()[:4000],
            },
            ensure_ascii=False,
        )
        result = kernel.run(SYSTEM, prompt)
        return _finish_hermes(store, captured, runtime, result)
    pair = _pair_from_capture(captured)
    seed = decode_seed(pair.seed)
    recovered = recover_formula(
        seed,
        pair.paths,
        pair.hex,
        store.formula,
        _seek_hint(captured),
    )
    if recovered.status in ("matched", "recovered") and recovered.formula is not None and not force_hermes:
        if recovered.status == "recovered":
            store.save_formula(recovered.formula)
            runtime.write_js(_hex_js_for_formula(recovered.formula, runtime))
        store.save_pair(pair)
        accepted = _accept(runtime, pair, recovered.formula)
        return {
            "ok": accepted.get("ok", False),
            "stage": recovered.status,
            "reason": recovered.reason,
            "formula": recovered.formula.to_dict(),
            "pair": {"hex": pair.hex, "curves_hash": pair.curves_hash, "source": pair.source},
            "accepted": accepted,
        }
    if not use_hermes:
        return {
            "ok": False,
            "stage": "needs_agent",
            "reason": recovered.reason,
            "capture": _public_capture(captured),
        }
    kernel = _kernel(store, captured, runtime, do_capture, capture_kwargs)
    prompt = json.dumps(
        {
            "task": "当前公式算不出官方 HEX。可重新 capture_page，或 recover_indices / 改 computeHex。verify_signature 通过才结束。",
            "reason": recovered.reason,
            "current_formula": store.formula.to_dict(),
            "official_hex": captured.get("hex"),
            "capture": _public_capture(captured),
            "chunk_urls": (captured.get("script_urls") or captured.get("chunks") or [])[:12],
        },
        ensure_ascii=False,
    )
    result = kernel.run(SYSTEM, prompt)
    return _finish_hermes(store, captured, runtime, result)


def fixture_from_pair_file(path: Path) -> dict[str, Any]:
    data = json.loads(Path(path).read_text(encoding="utf-8"))
    paths = list(data.get("paths") or [])
    return {
        "ok": True,
        "seed": data.get("seed"),
        "hex": data.get("hex"),
        "paths": paths,
        "curves_hash": data.get("curves_hash") or curves_hash(paths),
        "seeks": [{"value": (data.get("fingerprints") or {}).get("seek")}] if (data.get("fingerprints") or {}).get("seek") is not None else [{"value": 240}],
        "source": data.get("source") or str(path),
        "script_urls": [],
        "chunks": [],
        "browser": "fixture",
    }


def _finish_hermes(store: Store, captured: dict[str, Any], runtime: HotRuntime, result: Any) -> dict[str, Any]:
    pair_info: dict[str, Any] = {}
    accepted: dict[str, Any] = {"ok": False, "error": "还没有可用抓包"}
    if captured.get("seed") and captured.get("hex") and captured.get("paths"):
        pair = _pair_from_capture(captured)
        accepted = _accept(runtime, pair, store.formula)
        pair_info = {"hex": pair.hex, "curves_hash": pair.curves_hash, "source": pair.source}
    return {
        "ok": accepted.get("ok", False),
        "stage": "hermes",
        "content": result.content,
        "turns": result.turns,
        "formula": store.formula.to_dict(),
        "pair": pair_info,
        "capture": _public_capture(captured),
        "accepted": accepted,
    }


def _kernel(
    store: Store,
    captured: dict[str, Any],
    runtime: HotRuntime,
    capture_fn: Any,
    capture_kwargs: dict[str, Any] | None,
) -> HermesKernel:
    base = os.environ.get("GROK2API_BASE", "http://127.0.0.1:18000/v1")
    key = os.environ.get("GROK2API_KEY", "").strip()
    model = os.environ.get("GROK2API_MODEL", "grok-4.6")
    if not key:
        raise RuntimeError("Hermes 需要 GROK2API_KEY 指向 grok2api 客户端密钥")
    extra = dict(capture_kwargs or {})

    def _need_capture() -> dict[str, Any] | None:
        if captured.get("seed") and captured.get("hex") and captured.get("paths"):
            return None
        return {"error": "先调用 capture_page 抓同一页 seed/HEX/curves"}

    def capture_page(browser: str = "local", url: str = "https://grok.com/imagine", headed: bool = False) -> dict[str, Any]:
        kwargs = dict(extra)
        kwargs.update({"url": url, "headed": headed})
        if browser == "x2api" and extra.get("addr"):
            kwargs["addr"] = extra["addr"]
        result = capture_fn(browser=browser, **kwargs)
        captured.clear()
        captured.update(result or {})
        public = _public_capture(captured)
        if captured.get("ok"):
            public["hex"] = captured.get("hex")
            public["official_hex"] = captured.get("hex")
            public["chunk_urls"] = (captured.get("script_urls") or captured.get("chunks") or [])[:12]
            public["signer_chunks"] = [
                {"url": item.get("url"), "hits": item.get("hits")}
                for item in (captured.get("signer_chunks") or [])
            ]
        return public

    def inspect_capture() -> dict[str, Any]:
        missing = _need_capture()
        if missing:
            return missing
        seed = decode_seed(captured["seed"])
        anims = captured.get("anims") or []
        keyframes = ((anims[0] or {}).get("keyframes") if anims else []) or []
        return {
            "official_hex": captured.get("hex"),
            "hex_len": len(captured.get("hex") or ""),
            "salt": captured.get("salt"),
            "prefix": captured.get("prefix") or "",
            "digest_raw": (captured.get("digest_raw") or "")[:240],
            "seek": _seek_hint(captured),
            "sentry_release": captured.get("sentry_release") or "",
            "seed_bytes": list(seed),
            "path_count": len(captured.get("paths") or []),
            "keyframes": [
                {"color": item.get("color"), "transform": item.get("transform")}
                for item in keyframes[:4]
                if isinstance(item, dict)
            ],
            "signer_chunks": captured.get("signer_chunks") or [],
            "chunk_count": len(captured.get("chunks") or captured.get("script_urls") or []),
        }

    def find_signer_chunk() -> dict[str, Any]:
        existing = captured.get("signer_chunks") or []
        if existing:
            return {"ok": True, "chunks": existing}
        urls = list(captured.get("chunks") or []) + list(captured.get("script_urls") or [])
        from .capture import discover_signer_chunks

        found = discover_signer_chunks(urls)
        captured["signer_chunks"] = found
        return {"ok": bool(found), "chunks": found}

    def recover() -> dict[str, Any]:
        missing = _need_capture()
        if missing:
            return missing
        result = recover_formula(
            decode_seed(captured["seed"]),
            captured["paths"],
            captured["hex"],
            store.formula,
            _seek_hint(captured),
        )
        payload: dict[str, Any] = {"status": result.status, "reason": result.reason, "candidates": result.candidates}
        if result.formula:
            store.save_formula(result.formula)
            payload["formula"] = result.formula.to_dict()
            payload["hex_js"] = _hex_js_for_formula(result.formula)
        return payload

    def fetch_chunk(url: str) -> dict[str, Any]:
        if not url.startswith("https://cdn.grok.com/"):
            return {"error": "只允许 cdn.grok.com chunk"}
        with urllib.request.urlopen(url, timeout=20) as response:
            text = response.read()[:120_000].decode("utf-8", errors="replace")
        needles = ["obfiowerehiring", "animate", "4096", "x-statsig-id", "getComputedStyle"]
        hits = [item for item in needles if item in text]
        snippet = ""
        for item in ("obfiowerehiring", "W[", "animate"):
            idx = text.find(item)
            if idx >= 0:
                snippet = text[max(0, idx - 200) : idx + 400]
                break
        return {"url": url, "hits": hits, "snippet": snippet, "size": len(text)}

    def eval_hot_js(source: str) -> dict[str, Any]:
        missing = _need_capture()
        if missing:
            return missing
        seed = decode_seed(captured["seed"])
        hex_value = runtime.eval_js(source, seed, list(captured["paths"]))
        return {"hex": hex_value, "official": captured["hex"], "match": hex_value == captured["hex"]}

    def write_hot_js(source: str) -> dict[str, Any]:
        check = eval_hot_js(source)
        if check.get("error"):
            return check
        if not check["match"]:
            return {"ok": False, "error": "eval HEX 不等于官方 HEX，拒绝写入", **check}
        path = runtime.write_js(source)
        store.save_pair(_pair_from_capture(captured))
        return {"ok": True, "path": str(path), "hex": check["hex"]}

    def apply_pair() -> dict[str, Any]:
        missing = _need_capture()
        if missing:
            return missing
        seed = decode_seed(captured["seed"])
        hex_value = runtime.compute(seed, list(captured["paths"]), store.formula)
        if hex_value != captured["hex"]:
            return {"ok": False, "error": "当前热代码 HEX 不等于官方 HEX，不能只换 pair"}
        store.save_pair(_pair_from_capture(captured))
        return {"ok": True, "hex": captured["hex"]}

    def verify_signature() -> dict[str, Any]:
        missing = _need_capture()
        if missing:
            return missing
        return _accept(runtime, _pair_from_capture(captured), store.formula)

    tools = [
        Tool(
            "capture_page",
            "打开 grok.com/imagine，钩 digest 和 animate(4096)，抓同一页 seed、官方 HEX、4 条 curves。browser=local 或 x2api。",
            {
                "type": "object",
                "properties": {
                    "browser": {"type": "string", "enum": ["local", "x2api"]},
                    "url": {"type": "string"},
                    "headed": {"type": "boolean"},
                },
            },
            capture_page,
        ),
        Tool("read_hot_js", "读取签名器正在 eval 的 hot/hex.js", {"type": "object", "properties": {}}, lambda: {"source": runtime.current_js()}),
        Tool(
            "eval_hot_js",
            "用 Node vm.eval 跑一段 computeHex JS，和官方 HEX 比较。不写盘。",
            {"type": "object", "properties": {"source": {"type": "string"}}, "required": ["source"]},
            eval_hot_js,
        ),
        Tool(
            "write_hot_js",
            "仅当 eval 结果等于官方 HEX 时写入 hot/hex.js，签名器按 mtime 热加载",
            {"type": "object", "properties": {"source": {"type": "string"}}, "required": ["source"]},
            write_hot_js,
        ),
        Tool("inspect_capture", "看抓包：seed 字节、digest 明文、seek、keyframes、签名 chunk", {"type": "object", "properties": {}}, inspect_capture),
        Tool("find_signer_chunk", "在已加载 chunk 里搜 obfiowerehiring/animate(4096) 签名模块", {"type": "object", "properties": {}}, find_signer_chunk),
        Tool("recover_indices", "穷举 seed 下标，看能否对上官方 HEX。只覆盖当前 SVG HEX 公式。", {"type": "object", "properties": {}}, recover),
        Tool(
            "fetch_chunk",
            "拉取 grok CDN chunk，抽取 obfiowerehiring/W[n]/animate 附近源码",
            {"type": "object", "properties": {"url": {"type": "string"}}, "required": ["url"]},
            fetch_chunk,
        ),
        Tool("apply_pair", "热代码已匹配官方 HEX 时，只更新 seed/curves/官方 HEX", {"type": "object", "properties": {}}, apply_pair),
        Tool("verify_signature", "用当前热代码和抓包对照验签：HEX 和 70 字节壳都对才接受", {"type": "object", "properties": {}}, verify_signature),
    ]
    max_turns = int(os.environ.get("STATSIG_AGENT_MAX_TURNS") or "200")
    return HermesKernel(grok2api_complete(base, key, model, timeout=180), tools, max_turns=max_turns)


def _accept(runtime: HotRuntime, pair: Pair, formula: Formula) -> dict[str, Any]:
    seed = pair.seed_bytes()
    try:
        hot_hex = runtime.compute(seed, pair.paths, formula)
    except Exception as exc:
        return {"ok": False, "error": f"eval 失败: {exc}"}
    if hot_hex != pair.hex:
        return {"ok": False, "error": "热代码 HEX 不等于官方 HEX", "hex": hot_hex, "official": pair.hex}
    signature = build_statsig(seed, hot_hex, "POST", "/rest/app-chat/conversations/new", 1757419200, formula)
    info = inspect_statsig_id(signature, formula)
    if decode_seed(info["seed"]) != seed:
        return {"ok": False, "error": "签名壳 seed 对不上"}
    if info["mark"] != 3:
        return {"ok": False, "error": f"签名壳 mark={info['mark']}"}
    return {"ok": True, "hex": hot_hex, "statsig_id": signature, "mark": info["mark"]}


def _hex_js_for_formula(formula: Formula, runtime: HotRuntime | None = None) -> str:
    source = (Path(__file__).resolve().parents[1] / "hot" / "hex.js").read_text(encoding="utf-8")
    seeks = formula.seek_indices
    source = source.replace("seed[5] % 4", f"seed[{formula.path_index}] % {formula.path_mod}", 1)
    source = source.replace("seed[39] % 16", f"seed[{formula.seg_index}] % {formula.seg_mod}", 1)
    old_seek = "((seed[3] % 16) * (seed[31] % 16) * (seed[36] % 16))"
    new_seek = (
        f"((seed[{seeks[0]}] % {formula.seek_mod}) * "
        f"(seed[{seeks[1]}] % {formula.seek_mod}) * "
        f"(seed[{seeks[2]}] % {formula.seek_mod}))"
    )
    return source.replace(old_seek, new_seek, 1)


def _pair_from_capture(captured: dict[str, Any]) -> Pair:
    paths = list(captured.get("paths") or [])
    return Pair(
        seed=str(captured.get("seed") or ""),
        hex=str(captured.get("hex") or ""),
        paths=paths[:4],
        source=str(captured.get("source") or captured.get("url") or "live capture"),
        curves_hash=str(captured.get("curves_hash") or curves_hash(paths[:4])),
        updated_at=datetime.now(timezone.utc).isoformat(),
        fingerprints={
            "sentry_release": captured.get("sentry_release") or "",
            "seek": _seek_hint(captured),
            "salt": captured.get("salt") or "",
        },
    )


def _seek_hint(captured: dict[str, Any]) -> float | None:
    seeks = captured.get("seeks") or []
    if seeks and isinstance(seeks[0], dict) and seeks[0].get("value") is not None:
        try:
            return float(seeks[0]["value"])
        except (TypeError, ValueError):
            return None
    return None


def _public_capture(captured: dict[str, Any]) -> dict[str, Any]:
    return {
        "ok": captured.get("ok"),
        "browser": captured.get("browser"),
        "hex_len": len(captured.get("hex") or ""),
        "seed_len": len(captured.get("seed") or ""),
        "path_count": len(captured.get("paths") or []),
        "curves_hash": captured.get("curves_hash"),
        "sentry_release": captured.get("sentry_release"),
        "salt": captured.get("salt"),
        "seek": _seek_hint(captured),
        "prefix": captured.get("prefix") or "",
    }
