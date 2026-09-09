from __future__ import annotations

import base64
import hashlib
import math
import re
import secrets
from dataclasses import asdict, dataclass
from typing import Any, Iterable, Sequence

EPOCH = 1682924400
SALT = "obfiowerehiring"
MARK = 0x03
DURATION = 4096.0
SEED_LEN = 48
SHELL_LEN = 70


@dataclass(frozen=True)
class Formula:
    path_index: int = 5
    path_mod: int = 4
    seg_index: int = 39
    seg_mod: int = 16
    seek_indices: tuple[int, ...] = (3, 31, 36)
    seek_mod: int = 16
    seek_align: int = 10
    duration: float = DURATION
    epoch: int = EPOCH
    salt: str = SALT
    mark: int = MARK

    @classmethod
    def from_dict(cls, data: dict[str, Any] | None) -> "Formula":
        if not data:
            return cls()
        seeks = data.get("seek_indices", (3, 31, 36))
        return cls(
            path_index=int(data.get("path_index", 5)),
            path_mod=int(data.get("path_mod", 4)),
            seg_index=int(data.get("seg_index", 39)),
            seg_mod=int(data.get("seg_mod", 16)),
            seek_indices=tuple(int(v) for v in seeks),
            seek_mod=int(data.get("seek_mod", 16)),
            seek_align=int(data.get("seek_align", 10)),
            duration=float(data.get("duration", DURATION)),
            epoch=int(data.get("epoch", EPOCH)),
            salt=str(data.get("salt", SALT)),
            mark=int(data.get("mark", MARK)),
        )

    def to_dict(self) -> dict[str, Any]:
        payload = asdict(self)
        payload["seek_indices"] = list(self.seek_indices)
        return payload


DEFAULT_FORMULA = Formula()


def decode_seed(value: str) -> bytes:
    raw = (value or "").strip()
    if not raw:
        raise ValueError("Statsig metaContent 为空")
    padding = "=" * ((4 - len(raw) % 4) % 4)
    for candidate in (raw, raw + padding):
        try:
            decoded = base64.b64decode(candidate, validate=False)
        except Exception:
            continue
        if len(decoded) == SEED_LEN:
            return decoded
    raise ValueError("Statsig metaContent 不是 48 字节 seed")


def encode_raw_std(data: bytes) -> str:
    return base64.b64encode(data).decode("ascii").rstrip("=")


def decode_statsig_id(value: str) -> bytes:
    raw = (value or "").strip()
    if not raw:
        raise ValueError("x-statsig-id 为空")
    padding = "=" * ((4 - len(raw) % 4) % 4)
    for candidate in (raw, raw + padding):
        try:
            decoded = base64.b64decode(candidate, validate=False)
        except Exception:
            continue
        if len(decoded) == SHELL_LEN:
            return decoded
    raise ValueError("x-statsig-id 不是 70 字节")


def valid_statsig_id(value: str) -> bool:
    try:
        return len(decode_statsig_id(value)) == SHELL_LEN
    except ValueError:
        return False


def go_round(value: float) -> float:
    if value >= 0:
        return math.floor(value + 0.5)
    return math.ceil(value - 0.5)


def to_fixed(value: float, precision: int = 2) -> float:
    scale = 10**precision
    return go_round(value * scale) / scale


def number_to_hex(n: float) -> str:
    if n == 0:
        return "0"
    if n == math.trunc(n) and abs(n) < (1 << 53):
        return format(int(n), "x")
    return _float_to_hex_js(n)


def _float_to_hex_js(n: float) -> str:
    if n < 0:
        return "-" + _float_to_hex_js(-n)
    if n == 0:
        return "0"
    int_part = int(n)
    out = format(int_part, "x")
    frac = n - float(int_part)
    if frac <= 0:
        return out
    digits: list[str] = []
    for _ in range(16):
        if frac <= 1e-18:
            break
        frac *= 16
        digit = int(frac + 1e-12)
        if digit > 15:
            digit = 15
        digits.append("0123456789abcdef"[digit])
        frac -= float(digit)
        if frac < 0:
            frac = 0
    while digits and digits[-1] == "0":
        digits.pop()
    if digits:
        out += "." + "".join(digits)
    return out


def extract_numbers(seg: str) -> list[float]:
    parts = re.sub(r"[^\d]+", " ", seg).split()
    out: list[float] = []
    for part in parts:
        try:
            out.append(float(part))
        except ValueError:
            continue
    return out


def path_segments(path: str) -> list[list[float]]:
    if len(path) <= 9:
        return []
    segments: list[list[float]] = []
    for part in path[9:].split("C"):
        nums = extract_numbers(part)
        if nums:
            segments.append(nums)
    return segments


def scale_value(n: float, minimum: float, maximum: float, floor: bool) -> float:
    value = n * ((maximum - minimum) / 255.0) + minimum
    if floor:
        return math.floor(value)
    return to_fixed(value, 2)


def color_channel(start: float, end: float, progress: float) -> int:
    value = go_round(start + (end - start) * progress)
    if value < 0:
        return 0
    if value > 255:
        return 255
    return int(value)


def sample_cubic(t: float, a1: float, a2: float) -> float:
    return (((1 - 3 * a2 + 3 * a1) * t + (3 * a2 - 6 * a1)) * t * t) + 3 * a1 * t


def sample_cubic_derivative(t: float, a1: float, a2: float) -> float:
    return (3 * (1 - 3 * a2 + 3 * a1) * t + 2 * (3 * a2 - 6 * a1)) * t + 3 * a1


def cubic_bezier_y(x1: float, y1: float, x2: float, y2: float, x: float) -> float:
    if x <= 0:
        return 0
    if x >= 1:
        return 1
    t = x
    for _ in range(8):
        x_at_t = sample_cubic(t, x1, x2) - x
        if abs(x_at_t) < 1e-7:
            return sample_cubic(t, y1, y2)
        derivative = sample_cubic_derivative(t, x1, x2)
        if abs(derivative) < 1e-7:
            break
        t -= x_at_t / derivative
    lo, hi = 0.0, 1.0
    t = x
    for _ in range(32):
        x_at_t = sample_cubic(t, x1, x2)
        if abs(x_at_t - x) < 1e-7:
            return sample_cubic(t, y1, y2)
        if x > x_at_t:
            lo = t
        else:
            hi = t
        next_t = (hi + lo) / 2
        if next_t == t:
            break
        t = next_t
    return sample_cubic(t, y1, y2)


def aligned_seek(seed: bytes, formula: Formula) -> float:
    product = 1
    for index in formula.seek_indices:
        if index < 0 or index >= len(seed):
            raise ValueError("Statsig seek 下标越界")
        product *= int(seed[index]) % formula.seek_mod
    align = formula.seek_align or 1
    return go_round(product / align) * align


def hex_from_segment(seg: Sequence[float], seek: float, duration: float) -> str:
    if len(seg) < 11:
        raise ValueError("Statsig SVG 段过短")
    start_color = (seg[0], seg[1], seg[2])
    end_color = (seg[3], seg[4], seg[5])
    end_angle = scale_value(seg[6], 60, 360, True)
    x1 = scale_value(seg[7], 0, 1, False)
    y1 = scale_value(seg[8], -1, 1, False)
    x2 = scale_value(seg[9], 0, 1, False)
    y2 = scale_value(seg[10], -1, 1, False)
    progress = cubic_bezier_y(x1, y1, x2, y2, seek / duration)
    cos_v = math.cos(end_angle * progress * math.pi / 180)
    sin_v = math.sin(end_angle * progress * math.pi / 180)
    values = [
        float(color_channel(start_color[0], end_color[0], progress)),
        float(color_channel(start_color[1], end_color[1], progress)),
        float(color_channel(start_color[2], end_color[2], progress)),
        cos_v,
        sin_v,
        -sin_v,
        cos_v,
        0.0,
        0.0,
    ]
    encoded = "".join(number_to_hex(to_fixed(value, 2)) for value in values)
    return encoded.replace(".", "").replace("-", "")


def compute_hex(seed: bytes, paths: Sequence[str], formula: Formula | None = None) -> str:
    formula = formula or DEFAULT_FORMULA
    if len(seed) <= max(formula.path_index, formula.seg_index, *formula.seek_indices):
        raise ValueError("Statsig seed 过短")
    if not paths:
        raise ValueError("Statsig SVG 路径缺失")
    path_idx = int(seed[formula.path_index]) % formula.path_mod
    if path_idx >= len(paths):
        raise ValueError("Statsig SVG 路径越界")
    path = paths[path_idx]
    if not path:
        raise ValueError("Statsig SVG 路径缺失")
    segments = path_segments(path)
    if not segments:
        raise ValueError("Statsig SVG 路径无效")
    seg_idx = int(seed[formula.seg_index]) % formula.seg_mod
    if seg_idx >= len(segments):
        raise ValueError("Statsig SVG 段越界")
    return hex_from_segment(segments[seg_idx], aligned_seek(seed, formula), formula.duration)


def build_statsig(
    seed: bytes,
    hex_value: str,
    method: str,
    path: str,
    now_unix: int,
    formula: Formula | None = None,
    key: int | None = None,
) -> str:
    formula = formula or DEFAULT_FORMULA
    if len(seed) != SEED_LEN:
        raise ValueError("Statsig seed 必须是 48 字节")
    method = method.strip().upper()
    path = path.strip()
    hex_value = (hex_value or "").strip()
    if not method or not path or not hex_value:
        raise ValueError("Statsig 缺少 method、path 或 HEX")
    number = int(now_unix - formula.epoch) & 0xFFFFFFFF
    payload = f"{method}!{path}!{number}{formula.salt}{hex_value}".encode("ascii")
    digest = hashlib.sha256(payload).digest()
    if key is None:
        key = secrets.token_bytes(1)[0]
    out = bytearray(SHELL_LEN)
    out[0] = key
    for i, byte in enumerate(seed):
        out[1 + i] = byte ^ key
    out[49] = (number & 0xFF) ^ key
    out[50] = ((number >> 8) & 0xFF) ^ key
    out[51] = ((number >> 16) & 0xFF) ^ key
    out[52] = ((number >> 24) & 0xFF) ^ key
    for i in range(16):
        out[53 + i] = digest[i] ^ key
    out[69] = formula.mark ^ key
    return encode_raw_std(bytes(out))


def sign_with_meta(
    method: str,
    path: str,
    meta_content: str,
    now_unix: int,
    paths: Sequence[str],
    formula: Formula | None = None,
) -> str:
    seed = decode_seed(meta_content)
    hex_value = compute_hex(seed, paths, formula)
    return build_statsig(seed, hex_value, method, path, now_unix, formula)


def inspect_statsig_id(value: str, formula: Formula | None = None) -> dict[str, Any]:
    formula = formula or DEFAULT_FORMULA
    raw = decode_statsig_id(value)
    key = raw[0]
    seed = bytes(b ^ key for b in raw[1:49])
    number = (raw[49] ^ key) | ((raw[50] ^ key) << 8) | ((raw[51] ^ key) << 16) | ((raw[52] ^ key) << 24)
    mark = raw[69] ^ key
    return {
        "key": key,
        "seed": encode_raw_std(seed),
        "number": number,
        "now_unix": number + formula.epoch,
        "mark": mark,
        "sha16": bytes(b ^ key for b in raw[53:69]).hex(),
    }


def curves_hash(paths: Iterable[str]) -> str:
    joined = "\n".join(path.strip() for path in paths)
    return hashlib.sha256(joined.encode("utf-8")).hexdigest()
