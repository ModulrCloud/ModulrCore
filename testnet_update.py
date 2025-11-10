#!/usr/bin/env python3
import time
import json
import re
import shutil
import argparse
from pathlib import Path

V_DIR_PATTERN = re.compile(r"^V\d+$")
GENESIS_FILENAME = "genesis.json"
TARGET_KEY = "FIRST_EPOCH_START_TIMESTAMP"
DB_DIRNAME = "DATABASES"

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

def delete_databases_dir(vdir: Path) -> bool:
    db_path = vdir / DB_DIRNAME
    if db_path.exists() and db_path.is_dir():
        try:
            shutil.rmtree(db_path)
            print(f"[del] removed {db_path}")
            return True
        except Exception as e:
            print(f"[fail] cannot remove {db_path}: {e}")
    return False

def main():
    parser = argparse.ArgumentParser(description="Update genesis.json and remove DATABASES dirs in version folders.")
    parser.add_argument("root_dir", type=Path, help="Path to root directory containing V1, V2, ... subfolders")
    args = parser.parse_args()

    root_dir = args.root_dir
    if not root_dir.exists():
        raise SystemExit(f"Root dir not found: {root_dir}")

    millis = int(time.time() * 1000)
    print(millis)

    total = 0
    updated = 0
    deleted_db = 0

    for vdir in sorted(find_version_dirs(root_dir), key=lambda p: int(p.name[1:])):
        total += 1
        genesis = vdir / GENESIS_FILENAME

        if update_genesis(genesis, millis):
            updated += 1
        if delete_databases_dir(vdir):
            deleted_db += 1

    print(f"[summary] version dirs: {total}, updated: {updated}, db dirs deleted: {deleted_db}")

if __name__ == "__main__":
    main()
