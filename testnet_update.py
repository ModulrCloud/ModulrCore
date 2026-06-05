#!/usr/bin/env python3
import time
import json
import re
import shutil
import argparse
from pathlib import Path

V_DIR_PATTERN = re.compile(r"^V\d+$")
NETWORK_DB_DIR_PATTERN = re.compile(r"^[0-9a-fA-F]{64}$")
GENESIS_FILENAME = "genesis.json"
TARGET_KEY = "FIRST_EPOCH_START_TIMESTAMP"
DB_DIRNAME = "DATABASES"
STATE_DIRNAME = "STATE"

def find_version_dirs(root: Path):
    for p in root.iterdir():
        if p.is_dir() and V_DIR_PATTERN.match(p.name):
            yield p

def update_genesis(genesis_path: Path, millis: int) -> bool:
    if not genesis_path.exists():
        return False
    try:
        data = json.loads(genesis_path.read_text(encoding="utf-8"))
    except Exception as e:
        print(f"[skip] cannot parse JSON: {genesis_path} ({e})")
        return False

    old_value = data.get(TARGET_KEY)
    if old_value == millis:
        print(f"[ok] already up-to-date: {genesis_path}")
        return True

    data[TARGET_KEY] = millis
    try:
        genesis_path.write_text(
            json.dumps(data, ensure_ascii=False, indent=2) + "\n",
            encoding="utf-8"
        )
    except Exception as e:
        print(f"[fail] write failed for {genesis_path}: {e}")
        return False

    print(f"[upd] {genesis_path} :: {old_value} -> {millis}")
    return True

def delete_dir(path: Path, label: str) -> bool:
    if path.exists() and path.is_dir():
        try:
            shutil.rmtree(path)
            print(f"[del] removed {label}: {path}")
            return True
        except Exception as e:
            print(f"[fail] cannot remove {label} {path}: {e}")
    return False

def delete_runtime_dirs(vdir: Path, preserve_state: bool) -> int:
    deleted = 0

    # Old/anchor layout: all runtime DBs live under DATABASES.
    if delete_dir(vdir / DB_DIRNAME, DB_DIRNAME):
        deleted += 1

    # Current core layout: STATE is global, and block/metadata DBs are scoped
    # by network id in a 64-hex-character directory.
    if not preserve_state and delete_dir(vdir / STATE_DIRNAME, STATE_DIRNAME):
        deleted += 1

    for child in vdir.iterdir():
        if child.is_dir() and NETWORK_DB_DIR_PATTERN.match(child.name):
            if delete_dir(child, "network DB"):
                deleted += 1

    return deleted

def infer_anchor_chaindata(root_dir: Path) -> Path | None:
    try:
        modulr_root = root_dir.resolve().parent.parent
    except Exception:
        return None

    candidate = modulr_root / "modulr-anchors-core" / "XTESTNET_V1" / "V1"
    if candidate.exists() and candidate.is_dir():
        return candidate
    return None

def update_anchor_from_core_genesis(anchor_dir: Path, core_genesis_path: Path) -> bool:
    if not core_genesis_path.exists():
        print(f"[skip] core genesis for anchor update not found: {core_genesis_path}")
        return False

    anchor_genesis_path = anchor_dir / GENESIS_FILENAME
    anchor_core_genesis_path = anchor_dir / "core_genesis.json"
    if not anchor_genesis_path.exists():
        print(f"[skip] anchor genesis not found: {anchor_genesis_path}")
        return False

    try:
        core_genesis = json.loads(core_genesis_path.read_text(encoding="utf-8"))
        anchor_genesis = json.loads(anchor_genesis_path.read_text(encoding="utf-8"))
    except Exception as e:
        print(f"[skip] cannot parse genesis for anchor update ({e})")
        return False

    network_id = core_genesis.get("NETWORK_ID")
    first_epoch_start = core_genesis.get(TARGET_KEY)
    if not network_id or first_epoch_start is None:
        print(f"[skip] core genesis misses NETWORK_ID or {TARGET_KEY}: {core_genesis_path}")
        return False

    old_network_id = anchor_genesis.get("NETWORK_ID")
    old_first_epoch_start = anchor_genesis.get(TARGET_KEY)
    anchor_genesis["NETWORK_ID"] = network_id
    anchor_genesis[TARGET_KEY] = first_epoch_start

    try:
        anchor_genesis_path.write_text(
            json.dumps(anchor_genesis, ensure_ascii=False, indent=2) + "\n",
            encoding="utf-8",
        )
        shutil.copyfile(core_genesis_path, anchor_core_genesis_path)
    except Exception as e:
        print(f"[fail] cannot update anchor genesis files: {e}")
        return False

    print(
        f"[upd] anchor {anchor_genesis_path} :: "
        f"NETWORK_ID {old_network_id} -> {network_id}, "
        f"{TARGET_KEY} {old_first_epoch_start} -> {first_epoch_start}"
    )
    print(f"[upd] anchor core genesis copied: {anchor_core_genesis_path}")
    return True

def main():
    parser = argparse.ArgumentParser(
        description="Update genesis.json and remove runtime DB dirs in V1, V2, ... folders."
    )
    parser.add_argument("root_dir", type=Path, help="Path to root directory containing V1, V2, ... subfolders")
    parser.add_argument(
        "--preserve-state",
        action="store_true",
        help="Do not remove STATE directories (useful for recovery flows). Network-specific DB dirs are still removed.",
    )
    parser.add_argument(
        "--anchor-chaindata",
        type=Path,
        default=None,
        help="Path to anchor chaindata. If omitted, tries ../modulr-anchors-core/XTESTNET_V1/V1.",
    )
    parser.add_argument(
        "--no-anchor-update",
        action="store_true",
        help="Do not update anchor genesis/core_genesis or remove anchor DATABASES.",
    )
    args = parser.parse_args()

    root_dir = args.root_dir
    if not root_dir.exists():
        raise SystemExit(f"Root dir not found: {root_dir}")

    millis = int(time.time() * 1000)
    print(millis)

    total = 0
    updated = 0
    deleted_dirs = 0
    first_core_genesis = None

    for vdir in sorted(find_version_dirs(root_dir), key=lambda p: int(p.name[1:])):
        total += 1
        genesis = vdir / GENESIS_FILENAME
        if first_core_genesis is None and genesis.exists():
            first_core_genesis = genesis

        if update_genesis(genesis, millis):
            updated += 1
        deleted_dirs += delete_runtime_dirs(vdir, args.preserve_state)

    print(f"[summary] version dirs: {total}, updated: {updated}, runtime dirs deleted: {deleted_dirs}")

    if args.no_anchor_update:
        return

    anchor_dir = args.anchor_chaindata or infer_anchor_chaindata(root_dir)
    if anchor_dir is None:
        print("[skip] anchor chaindata was not provided and could not be auto-detected")
        return
    if not anchor_dir.exists():
        print(f"[skip] anchor chaindata not found: {anchor_dir}")
        return
    if first_core_genesis is None:
        print("[skip] no core genesis found for anchor update")
        return

    if update_anchor_from_core_genesis(anchor_dir, first_core_genesis):
        delete_dir(anchor_dir / DB_DIRNAME, f"anchor {DB_DIRNAME}")

if __name__ == "__main__":
    main()
