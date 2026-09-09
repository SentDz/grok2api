from __future__ import annotations

import json
import re
from typing import Any

SCRIPT_SRC = re.compile(r"""(?:src|href)=["'](https://cdn\.grok\.com/_next/static/chunks/[^"']+\.js)["']""")
SENTRY_RELEASE = re.compile(r"grok-web(?:@|%40)([0-9a-f]{7,40})", re.I)


def extract_curve_paths(html: str) -> list[str]:
    groups = extract_curve_groups(html)
    if len(groups) < 4:
        return []
    out: list[str] = []
    for group in groups[:4]:
        path = curve_group_to_path(group)
        if not path.startswith("M 10,30 C") or path.count("C") < 4:
            return []
        out.append(path)
    return out


def extract_curve_groups(html: str) -> list[list[dict[str, Any]]]:
    marker = '"curves":'
    idx = html.find(marker)
    if idx < 0:
        marker = '\\"curves\\":'
        idx = html.find(marker)
    if idx < 0:
        return []
    window = html[idx : idx + 80000].replace('\\"', '"')
    start = window.find("[[")
    if start < 0:
        return []
    raw = slice_balanced_array(window[start:])
    if not raw:
        return []
    try:
        groups = json.loads(raw)
    except json.JSONDecodeError:
        return []
    if not isinstance(groups, list) or len(groups) < 4:
        return []
    return groups


def slice_balanced_array(src: str) -> str:
    depth = 0
    for i, ch in enumerate(src):
        if ch == "[":
            depth += 1
        elif ch == "]":
            depth -= 1
            if depth == 0:
                return src[: i + 1]
    return ""


def curve_group_to_path(group: list[dict[str, Any]]) -> str:
    if not group:
        return ""
    parts: list[str] = []
    for seg in group:
        color = list(seg.get("color") or [0, 0, 0, 0, 0, 0]) + [0, 0, 0, 0, 0, 0]
        bezier = list(seg.get("bezier") or [0, 0, 0, 0]) + [0, 0, 0, 0]
        deg = int(seg.get("deg") or 0)
        parts.append(
            f" {int(color[0])},{int(color[1])} {int(color[2])},{int(color[3])} {int(color[4])},{int(color[5])} h {deg} s {int(bezier[0])},{int(bezier[1])} {int(bezier[2])},{int(bezier[3])}"
        )
    out = "M 10,30 C"
    for i, part in enumerate(parts):
        if i:
            out += " C"
        out += part
    return out


def extract_script_urls(html: str) -> list[str]:
    seen: set[str] = set()
    urls: list[str] = []
    for match in SCRIPT_SRC.findall(html):
        if match not in seen:
            seen.add(match)
            urls.append(match)
    return urls


def extract_sentry_release(html: str) -> str:
    match = SENTRY_RELEASE.search(html)
    if not match:
        return ""
    return "grok-web@" + match.group(1)
