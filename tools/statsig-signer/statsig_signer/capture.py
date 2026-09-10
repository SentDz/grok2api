from __future__ import annotations

import json
import os
import time
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from .algorithm import curves_hash
from .htmlutil import extract_curve_paths, extract_script_urls, extract_sentry_release

SIGNER_MARKERS = ("obfiowerehiring", "x-statsig-id", "animate", "4096", "getComputedStyle", "currentTime")

HOOK_JS = r"""
() => {
  window.__sig = window.__sig || { digest: null, seeks: [], anims: [], chunks: [], to16: [] };
  if (window.__sig._hooked) return true;
  window.__sig._hooked = true;
  const origTS = Number.prototype.toString;
  Number.prototype.toString = function (radix) {
    const out = origTS.apply(this, arguments);
    try {
      if (radix === 16) {
        const buf = window.__sig.to16;
        buf.push(out);
        if (buf.length > 64) buf.splice(0, buf.length - 64);
      }
    } catch (e) {}
    return out;
  };
  const origDigest = crypto.subtle.digest.bind(crypto.subtle);
  crypto.subtle.digest = function (algo, data) {
    try {
      const bytes = data instanceof ArrayBuffer ? new Uint8Array(data) : new Uint8Array(data.buffer || data);
      const text = new TextDecoder().decode(bytes);
      const salt = "obfiowerehiring";
      const idx = text.indexOf(salt);
      let seed = "";
      for (const meta of document.querySelectorAll("meta")) {
        const name = meta.getAttribute("name") || "";
        if (name.replace(/[\u2010\u2011\u2012\u2013\u2014\u2015]/g, "-").includes("grok-site-verification")) {
          seed = meta.getAttribute("content") || "";
        }
      }
      if (idx >= 0) {
        const hex = text.slice(idx + salt.length);
        const stripped = String(hex).replace(/[.-]/g, "");
        const parts = window.__sig.to16 || [];
        const joined = parts.join("").replace(/[.-]/g, "");
        const hexAgree = Boolean(stripped) && joined.indexOf(stripped) >= 0;
        window.__sig.digest = {
          seed,
          hex,
          hexFromToString: hexAgree ? stripped : "",
          hexAgree,
          to16Count: parts.length,
          to16JoinedLen: joined.length,
          salt,
          prefix: text.slice(0, Math.min(idx + 48, 240)),
        };
      } else if (!window.__sig.digestRaw) {
        window.__sig.digestRaw = text.slice(0, 240);
      }
    } catch (e) {}
    return origDigest(algo, data);
  };
  const origAnimate = Element.prototype.animate;
  Element.prototype.animate = function (keyframes, options) {
    const anim = origAnimate.apply(this, arguments);
    try {
      const duration = typeof options === "number" ? options : options && options.duration;
      if (duration === 4096) {
        const kfs = anim.effect && anim.effect.getKeyframes ? anim.effect.getKeyframes() : [];
        window.__sig.anims.push({ duration, keyframes: kfs, via: "animate" });
        const desc = Object.getOwnPropertyDescriptor(Animation.prototype, "currentTime");
        if (desc && desc.set) {
          Object.defineProperty(anim, "currentTime", {
            configurable: true,
            get() { return desc.get.call(this); },
            set(value) {
              window.__sig.seeks.push({ value, href: location.href, via: "currentTime" });
              return desc.set.call(this, value);
            },
          });
        }
      }
    } catch (e) {}
    return anim;
  };
  return true;
}
"""

PROBE_JS = r"""
async () => {
  try {
    await fetch("/rest/modes", { method: "POST", headers: { "content-type": "application/json" }, body: "{}" });
  } catch (e) {}
  try {
    const anims = document.getAnimations ? document.getAnimations() : [];
    for (const anim of anims) {
      const timing = anim.effect && anim.effect.getTiming ? anim.effect.getTiming() : null;
      const duration = timing && timing.duration;
      if (duration === 4096 && anim.currentTime != null) {
        window.__sig.seeks.push({ value: anim.currentTime, href: location.href, via: "getAnimations" });
        if (anim.effect && anim.effect.getKeyframes) {
          window.__sig.anims.push({ duration, keyframes: anim.effect.getKeyframes(), via: "getAnimations" });
        }
      }
    }
  } catch (e) {}
  return window.__sig || null;
}
"""


def load_secrets() -> dict[str, Any]:
    path = os.environ.get("STATSIG_SECRETS", "/tmp/g2a-local-test/browser.secrets.json")
    if os.path.exists(path):
        return json.loads(Path(path).read_text(encoding="utf-8"))
    sso = os.environ.get("GROK_SSO", "").strip()
    if not sso:
        return {}
    return {
        "local16_sso": sso,
        "proxy_server": os.environ.get("PROXY_SERVER", ""),
        "proxy_username_tmpl": os.environ.get("PROXY_USERNAME_TMPL", ""),
        "proxy_password": os.environ.get("PROXY_PASSWORD", ""),
        "local17_sticky": os.environ.get("PROXY_STICKY", "statsig-repair"),
    }


def capture(browser: str = "local", **kwargs: Any) -> dict[str, Any]:
    kind = (browser or "local").strip().lower()
    if kind == "x2api":
        from .x2api import capture_x2api

        return capture_x2api(**kwargs)
    return capture_local(**kwargs)


def capture_local(url: str = "https://grok.com/imagine", headed: bool = False, timeout_ms: int = 45000) -> dict[str, Any]:
    try:
        from playwright.sync_api import sync_playwright
    except ImportError as exc:
        raise RuntimeError("本机抓包需要 playwright：pip install playwright && playwright install chromium") from exc

    secrets = load_secrets()
    result: dict[str, Any] = {"ok": False, "browser": "local", "url": url}
    with sync_playwright() as p:
        launch: dict[str, Any] = {
            "headless": not headed,
            "args": ["--disable-blink-features=AutomationControlled"],
            "ignore_default_args": ["--enable-automation"],
        }
        exe = os.environ.get("PLAYWRIGHT_CHROMIUM", "").strip()
        if exe:
            launch["executable_path"] = exe
        browser = p.chromium.launch(**launch)
        try:
            context_opts: dict[str, Any] = {
                "viewport": {"width": 1280, "height": 800},
                "locale": "zh-CN",
                "user_agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36",
            }
            if secrets.get("proxy_server"):
                username = (secrets.get("proxy_username_tmpl") or "").replace(
                    "{account}", secrets.get("local17_sticky") or "statsig-repair"
                )
                context_opts["proxy"] = {
                    "server": secrets["proxy_server"],
                    "username": username,
                    "password": secrets.get("proxy_password") or "",
                }
            context = browser.new_context(**context_opts)
            context.add_init_script(_init_script())
            sso = secrets.get("local16_sso") or secrets.get("grokx_sso") or ""
            if sso:
                context.add_cookies(
                    [
                        {"name": "sso", "value": sso, "domain": "grok.com", "path": "/", "secure": True, "httpOnly": True},
                        {"name": "sso-rw", "value": sso, "domain": "grok.com", "path": "/", "secure": True, "httpOnly": True},
                    ]
                )
            page = context.new_page()
            chunks: list[str] = []

            def on_response(response: Any) -> None:
                resp_url = response.url
                if "cdn.grok.com/_next/static/chunks/" in resp_url and resp_url.endswith(".js") and len(chunks) < 200:
                    chunks.append(resp_url)

            page.on("response", on_response)
            started = time.monotonic()
            page.goto(url, wait_until="domcontentloaded", timeout=timeout_ms)
            hooked = None
            polls = 0
            for i in range(20):
                polls = i + 1
                if i in (0, 2, 6, 12):
                    page.evaluate(PROBE_JS)
                hooked = page.evaluate("() => window.__sig")
                if hooked and hooked.get("digest") and hooked["digest"].get("hex"):
                    break
                page.wait_for_timeout(250)
            html = page.content()
            paths = extract_curve_paths(html)
            digest = (hooked or {}).get("digest") or {}
            result.update(
                {
                    "ok": bool(digest.get("seed") and digest.get("hex") and len(paths) == 4),
                    "seed": digest.get("seed") or "",
                    "hex": digest.get("hex") or "",
                    "hex_from_tostring": digest.get("hexFromToString") or "",
                    "hex_agree": bool(digest.get("hexAgree")),
                    "to16_count": digest.get("to16Count"),
                    "to16_joined_len": digest.get("to16JoinedLen"),
                    "salt": digest.get("salt") or "",
                    "prefix": digest.get("prefix") or "",
                    "paths": paths,
                    "seeks": (hooked or {}).get("seeks") or [],
                    "anims": (hooked or {}).get("anims") or [],
                    "chunks": chunks,
                    "script_urls": extract_script_urls(html),
                    "sentry_release": extract_sentry_release(html),
                    "curves_hash": curves_hash(paths) if paths else "",
                    "digest_raw": (hooked or {}).get("digestRaw") or "",
                    "polls": polls,
                    "elapsed_ms": int((time.monotonic() - started) * 1000),
                }
            )
            result["signer_chunks"] = discover_signer_chunks(result.get("chunks") or result.get("script_urls") or [])
        finally:
            browser.close()
    return result


def discover_signer_chunks(urls: list[str], limit: int = 32) -> list[dict[str, Any]]:
    hits: list[dict[str, Any]] = []
    seen: set[str] = set()
    for url in urls:
        if not url or url in seen or not url.startswith("https://cdn.grok.com/"):
            continue
        seen.add(url)
        if len(seen) > limit:
            break
        try:
            with urllib.request.urlopen(url, timeout=8) as response:
                text = response.read()[:200_000].decode("utf-8", errors="replace")
        except Exception:
            continue
        found = [marker for marker in SIGNER_MARKERS if marker in text]
        if "obfiowerehiring" not in found and not ("animate" in found and "4096" in found):
            continue
        snippet = ""
        for marker in ("obfiowerehiring", "W[", "animate"):
            idx = text.find(marker)
            if idx >= 0:
                snippet = text[max(0, idx - 240) : idx + 500]
                break
        hits.append({"url": url, "hits": found, "snippet": snippet, "size": len(text)})
        if len(hits) >= 3:
            break
    return hits


def _init_script() -> str:
    # add_init_script wants a statement, not an arrow function wrapper.
    body = HOOK_JS.strip()
    if body.startswith("() =>"):
        body = body[body.find("{") + 1 : body.rfind("}")]
    return body


def verify_record(captured: dict[str, Any], extra: dict[str, Any] | None = None) -> dict[str, Any]:
    """Redacted capture log: no SSO, seed only as length."""
    seeks = captured.get("seeks") or []
    anims = captured.get("anims") or []
    record = {
        "ok": bool(captured.get("ok")),
        "observed_at": datetime.now(timezone.utc).isoformat(),
        "url": captured.get("url") or "",
        "browser": captured.get("browser") or "",
        "sentry_release": captured.get("sentry_release") or "",
        "curves_hash": captured.get("curves_hash") or "",
        "salt": captured.get("salt") or "",
        "hex": captured.get("hex") or "",
        "hex_from_tostring": captured.get("hex_from_tostring") or "",
        "hex_agree": bool(captured.get("hex_agree")),
        "to16_count": captured.get("to16_count"),
        "to16_joined_len": captured.get("to16_joined_len"),
        "hex_len": len(captured.get("hex") or ""),
        "seed_len": len(captured.get("seed") or ""),
        "path_count": len(captured.get("paths") or []),
        "seeks": [
            {"value": item.get("value"), "via": item.get("via")}
            for item in seeks[:8]
            if isinstance(item, dict)
        ],
        "anims_via": [item.get("via") for item in anims[:8] if isinstance(item, dict)],
        "polls": captured.get("polls"),
        "elapsed_ms": captured.get("elapsed_ms"),
    }
    if extra:
        record.update(extra)
    return record
