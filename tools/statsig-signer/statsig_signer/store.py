from __future__ import annotations

import json
import os
import tempfile
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from .algorithm import Formula, curves_hash, decode_seed

PACKAGE_DIR = Path(__file__).resolve().parent
DEFAULT_DATA_DIR = PACKAGE_DIR.parent / "data"


def data_dir() -> Path:
    override = os.environ.get("STATSIG_DATA_DIR", "").strip()
    return Path(override).expanduser() if override else DEFAULT_DATA_DIR


@dataclass
class Pair:
    seed: str
    hex: str
    paths: list[str]
    source: str = ""
    curves_hash: str = ""
    updated_at: str = ""
    fingerprints: dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> "Pair":
        paths = [str(item) for item in data.get("paths") or [] if str(item).strip()]
        seed = str(data.get("seed") or "").strip()
        hex_value = str(data.get("hex") or "").strip()
        if len(paths) < 4 or not seed or not hex_value:
            raise ValueError("pair.json 需要 seed、hex 和 4 条 curves")
        return cls(
            seed=seed,
            hex=hex_value,
            paths=paths[:4],
            source=str(data.get("source") or ""),
            curves_hash=str(data.get("curves_hash") or curves_hash(paths[:4])),
            updated_at=str(data.get("updated_at") or ""),
            fingerprints=dict(data.get("fingerprints") or {}),
        )

    def to_dict(self) -> dict[str, Any]:
        return {
            "seed": self.seed,
            "hex": self.hex,
            "paths": self.paths,
            "source": self.source,
            "curves_hash": self.curves_hash or curves_hash(self.paths),
            "updated_at": self.updated_at,
            "fingerprints": self.fingerprints,
        }

    def seed_bytes(self) -> bytes:
        return decode_seed(self.seed)


class Store:
    def __init__(self, directory: Path | None = None) -> None:
        self.directory = Path(directory) if directory else data_dir()
        self.formula_path = self.directory / "formula.json"
        self.pair_path = self.directory / "pair.json"
        self._formula = Formula()
        self._pair: Pair | None = None
        self._formula_mtime = 0.0
        self._pair_mtime = 0.0
        self.reload(force=True)

    @property
    def formula(self) -> Formula:
        self.reload()
        return self._formula

    @property
    def pair(self) -> Pair:
        self.reload()
        if self._pair is None:
            raise FileNotFoundError(f"缺少 {self.pair_path}")
        return self._pair

    def reload(self, force: bool = False) -> None:
        self.directory.mkdir(parents=True, exist_ok=True)
        formula_mtime = _mtime(self.formula_path)
        pair_mtime = _mtime(self.pair_path)
        if force or formula_mtime != self._formula_mtime:
            if self.formula_path.exists():
                self._formula = Formula.from_dict(json.loads(self.formula_path.read_text(encoding="utf-8")))
            else:
                self._formula = Formula()
                atomic_write_json(self.formula_path, self._formula.to_dict())
                formula_mtime = _mtime(self.formula_path)
            self._formula_mtime = formula_mtime
        if force or pair_mtime != self._pair_mtime:
            if self.pair_path.exists():
                self._pair = Pair.from_dict(json.loads(self.pair_path.read_text(encoding="utf-8")))
            self._pair_mtime = pair_mtime

    def save_formula(self, formula: Formula) -> None:
        atomic_write_json(self.formula_path, formula.to_dict())
        self._formula = formula
        self._formula_mtime = _mtime(self.formula_path)

    def save_pair(self, pair: Pair) -> None:
        if not pair.curves_hash:
            pair.curves_hash = curves_hash(pair.paths)
        atomic_write_json(self.pair_path, pair.to_dict())
        self._pair = pair
        self._pair_mtime = _mtime(self.pair_path)


def atomic_write_json(path: Path, payload: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    encoded = json.dumps(payload, ensure_ascii=False, indent=2) + "\n"
    fd, tmp_name = tempfile.mkstemp(prefix=path.name, suffix=".tmp", dir=str(path.parent))
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            handle.write(encoded)
        os.replace(tmp_name, path)
    except Exception:
        try:
            os.unlink(tmp_name)
        except OSError:
            pass
        raise


def _mtime(path: Path) -> float:
    try:
        return path.stat().st_mtime
    except OSError:
        return 0.0
