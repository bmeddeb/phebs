#!/usr/bin/env python3
"""Materialize frozen public bytes and evaluator inputs; never run predictions."""

import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import shutil
import stat
import time
import urllib.request


HERE = Path(__file__).resolve().parent
INPUT_NAMES = (
    "go.mod", "go.sum", "layout-snapshot.json",
    "generated-from-snapshot.json", "unit-snapshot.json",
)


def digest(raw):
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def clean_path(value):
    if (not isinstance(value, str) or not value or "\\" in value
            or value.startswith("/") or str(PurePosixPath(value)) != value
            or any(part in (".", "..") for part in value.split("/"))):
        raise ValueError("noncanonical relative source path")
    return value


def validate_lock(lock):
    if lock.get("schema") != "phebs-t503-public-population-v1":
        raise ValueError("unknown population schema")
    upstream = lock["upstream"]
    if upstream["repository"] != "https://github.com/grpc/grpc-go":
        raise ValueError("unexpected public repository")
    commit = upstream["commit"]
    if len(commit) != 40 or any(ch not in "0123456789abcdef" for ch in commit):
        raise ValueError("invalid immutable upstream commit")
    files = lock["files"]
    if not files or len(files) > 32:
        raise ValueError("invalid public file count")
    paths, destinations = set(), set()
    total = 0
    for row in files:
        path = clean_path(row["path"])
        if path in paths:
            raise ValueError("duplicate public path")
        paths.add(path)
        if row["url"] != f"https://raw.githubusercontent.com/grpc/grpc-go/{commit}/{path}":
            raise ValueError("source URL is not the exact immutable public path")
        size = row["bytes"]
        if type(size) is not int or not 0 < size <= lock["bounds"]["download_file_bytes"]:
            raise ValueError("public file exceeds download bound")
        total += size
        for key, prefix, length in (("sha256", "sha256:", 64), ("git_blob", "", 40)):
            value = row[key]
            suffix = value[len(prefix):]
            if (not value.startswith(prefix) or len(suffix) != length
                    or any(ch not in "0123456789abcdef" for ch in suffix)):
                raise ValueError("invalid public content digest")
        destination = row["derived_path"]
        if destination is not None:
            clean_path(destination)
            if destination != path.removeprefix("examples/"):
                raise ValueError("root mapping changed")
            if destination in destinations or destination in INPUT_NAMES:
                raise ValueError("duplicate or conflicting derived path")
            destinations.add(destination)
        elif row["role"] != "upstream-module-context":
            raise ValueError("unexplained excluded public input")
    if total > lock["bounds"]["download_total_bytes"]:
        raise ValueError("public population exceeds total download bound")


def verify_bytes(row, raw):
    blob = hashlib.sha1(f"blob {len(raw)}\0".encode() + raw).hexdigest()
    if (len(raw) != row["bytes"] or digest(raw) != row["sha256"]
            or blob != row["git_blob"]):
        raise ValueError("public source bytes disagree with frozen population")


def verify_labels(labels, sources):
    if labels.get("schema") != "phebs-t503-source-labels-v1":
        raise ValueError("unknown source-label schema")
    seen = set()
    for row in labels["sites"]:
        if row["site_id"] in seen:
            raise ValueError("duplicate source label")
        seen.add(row["site_id"])
        raw = sources[clean_path(row["upstream_path"])]
        start, end = row["start_byte"], row["end_byte"]
        if (type(start) is not int or type(end) is not int
                or not 0 <= start < end < len(raw)
                or raw[start:end] != row["selector"].encode()
                or raw[end:end + 1] != b"("
                or digest(raw) != row["upstream_blob_sha256"]
                or raw[:start].count(b"\n") + 1 != row["start_line"]
                or raw[:end - 1].count(b"\n") + 1 != row["end_line"]):
            raise ValueError("source label locator does not bind exact public bytes")


def read_cache(root, path):
    current = root
    for part in clean_path(path).split("/"):
        current = current / part
        if current.is_symlink():
            raise ValueError("source cache contains a symlink")
    if not stat.S_ISREG(current.stat().st_mode):
        raise ValueError("source cache entry is not regular")
    return current.read_bytes()


def materialize(out, cache=None):
    lock = json.loads((HERE / "population.lock.json").read_text())
    labels = json.loads((HERE / "labels.json").read_text())
    validate_lock(lock)
    if out.exists() or out.is_symlink():
        raise ValueError("output must be a new directory")
    if cache is not None and (not cache.is_dir() or cache.is_symlink()):
        raise ValueError("source cache must be a real directory")
    sources = {}
    deadline = time.monotonic() + 60
    for row in lock["files"]:
        if cache is not None:
            raw = read_cache(cache, row["path"])
        else:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise ValueError("public input preparation exceeded60s")
            with urllib.request.urlopen(row["url"], timeout=min(20, remaining)) as response:
                raw = response.read(lock["bounds"]["download_file_bytes"] + 1)
        verify_bytes(row, raw)
        sources[row["path"]] = raw
    verify_labels(labels, sources)
    out.mkdir(mode=0o700)
    try:
        for row in lock["files"]:
            if row["derived_path"] is None:
                continue
            destination = out / row["derived_path"]
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes(sources[row["path"]])
        for name in INPUT_NAMES:
            shutil.copyfile(HERE / "inputs" / name, out / name)
        origin = {
            "schema": "phebs-t503-evaluator-origin-v1",
            "upstream": lock["upstream"],
            "population_digest": digest((HERE / "population.lock.json").read_bytes()),
            "labels_digest": digest((HERE / "labels.json").read_bytes()),
            "root_mapping": "Uniformly remove examples/; source bytes unchanged.",
            "source_files": lock["files"],
            "evaluator_inputs": {name: digest((HERE / "inputs" / name).read_bytes()) for name in INPUT_NAMES},
            "qualification": "Evaluator assembly only; no Git commit, SCIP, Phebs prediction or score produced.",
        }
        (out / "origin.json").write_text(json.dumps(origin, indent=2) + "\n")
    except BaseException:
        shutil.rmtree(out)
        raise
    return origin


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--source-cache", type=Path, help="Read-only cache with original upstream paths; otherwise download frozen URLs")
    args = parser.parse_args()
    materialize(args.out, args.source_cache)
    print("Frozen public bytes and evaluator inputs verified; no predictions or scores produced.")
