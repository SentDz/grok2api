from __future__ import annotations

from dataclasses import dataclass, field
from itertools import product
from typing import Any, Sequence

from .algorithm import Formula, aligned_seek, compute_hex, go_round, hex_from_segment, path_segments

SEEK_MAX = 4090


@dataclass
class RecoverResult:
    status: str
    formula: Formula | None = None
    hex: str = ""
    reason: str = ""
    candidates: list[dict[str, Any]] = field(default_factory=list)


def recover_formula(
    seed: bytes,
    paths: Sequence[str],
    official_hex: str,
    current: Formula | None = None,
    seek_hint: float | None = None,
) -> RecoverResult:
    current = current or Formula()
    official = (official_hex or "").strip()
    if not official:
        return RecoverResult(status="failed", reason="缺少官方 HEX")
    try:
        got = compute_hex(seed, paths, current)
    except ValueError as exc:
        got = ""
        current_error = str(exc)
    else:
        current_error = ""
        if got == official:
            return RecoverResult(status="matched", formula=current, hex=got, reason="当前公式仍匹配官方 HEX")

    direct = _search_direct(seed, paths, official, seek_hint)
    if not direct:
        reason = "穷举 path/seg/seek 得不到官方 HEX"
        if current_error:
            reason += f"；当前公式: {current_error}"
        return RecoverResult(status="needs_agent", hex=official, reason=reason)

    inverted = _invert_indices(seed, current, direct)
    if inverted is None:
        return RecoverResult(
            status="needs_agent",
            hex=official,
            reason="找到了 path/seg/seek，但无法反解 seed 下标",
            candidates=[direct],
        )
    try:
        verified = compute_hex(seed, paths, inverted)
    except ValueError as exc:
        return RecoverResult(status="needs_agent", reason=f"反解公式无法复算: {exc}", candidates=[direct])
    if verified != official:
        return RecoverResult(status="needs_agent", reason="反解公式复算 HEX 不一致", candidates=[direct])
    return RecoverResult(status="recovered", formula=inverted, hex=verified, reason="穷举下标后与官方 HEX 一致")


def _search_direct(
    seed: bytes,
    paths: Sequence[str],
    official_hex: str,
    seek_hint: float | None,
) -> dict[str, Any] | None:
    seeks: list[float]
    if seek_hint is not None:
        seeks = [float(seek_hint)]
    else:
        seeks = [float(v) for v in range(0, SEEK_MAX + 1, 10)]
    for path_i, path in enumerate(paths):
        segments = path_segments(path)
        limit = min(len(segments), 16)
        for seg_i in range(limit):
            for seek in seeks:
                try:
                    hex_value = hex_from_segment(segments[seg_i], seek, 4096.0)
                except ValueError:
                    continue
                if hex_value == official_hex:
                    return {"path": path_i, "seg": seg_i, "seek": seek}
    return None


def _formula_matches_direct(seed: bytes, formula: Formula, direct: dict[str, Any]) -> bool:
    try:
        path_i = int(seed[formula.path_index]) % formula.path_mod
        seg_i = int(seed[formula.seg_index]) % formula.seg_mod
        return path_i == int(direct["path"]) and seg_i == int(direct["seg"]) and aligned_seek(seed, formula) == float(direct["seek"])
    except (ValueError, IndexError):
        return False


def _invert_indices(seed: bytes, current: Formula, direct: dict[str, Any]) -> Formula | None:
    path_i = int(direct["path"])
    seg_i = int(direct["seg"])
    seek = float(direct["seek"])
    default = Formula()
    if _formula_matches_direct(seed, default, direct):
        return default
    if _formula_matches_direct(seed, current, direct):
        return current
    path_bytes = [i for i in range(len(seed)) if int(seed[i]) % current.path_mod == path_i]
    seg_bytes = [i for i in range(len(seed)) if int(seed[i]) % current.seg_mod == seg_i]
    if default.path_index in path_bytes:
        path_bytes = [default.path_index] + [i for i in path_bytes if i != default.path_index]
    if default.seg_index in seg_bytes:
        seg_bytes = [default.seg_index] + [i for i in seg_bytes if i != default.seg_index]
    seek_mod = current.seek_mod
    align = current.seek_align or 1
    n = len(seed)
    for a, b, c in product(range(n), repeat=3):
        product_v = (int(seed[a]) % seek_mod) * (int(seed[b]) % seek_mod) * (int(seed[c]) % seek_mod)
        if go_round(product_v / align) * align == seek:
            path_index = path_bytes[0] if path_bytes else current.path_index
            seg_index = seg_bytes[0] if seg_bytes else current.seg_index
            return Formula(
                path_index=path_index,
                path_mod=current.path_mod,
                seg_index=seg_index,
                seg_mod=current.seg_mod,
                seek_indices=(a, b, c),
                seek_mod=current.seek_mod,
                seek_align=current.seek_align,
                duration=current.duration,
                epoch=current.epoch,
                salt=current.salt,
                mark=current.mark,
            )
    return None
