#!/usr/bin/env python3
"""Fetch immutable Apache-2.0 corpus blobs only; never index or score them."""
import hashlib
import io
import json
from pathlib import Path
import tarfile
import urllib.request

ROOT = Path(__file__).resolve().parent
PINS = {
    "public": ("jaegertracing/jaeger-client-go", "8d8e8fcfd04de42b8482476abac6a902fca47c18"),
    "idl": ("jaegertracing/jaeger-idl", "0daa7197c60ca879532826035be0fb238d98e713"),
}
APP = {"jaeger_tag.go", "testutils/sampling_manager.go"}
GENERATED = {f"thrift-gen/{pkg}/{name}" for pkg in ("jaeger", "sampling") for name in (f"{pkg}.go", f"{pkg}-consts.go", "GoUnusedProtection__.go")}

def digest(raw):
    return "sha256:" + hashlib.sha256(raw).hexdigest()

def fetch(url):
    with urllib.request.urlopen(url, timeout=30) as response:
        raw = response.read((8 << 20) + 1)
    if len(raw) > 8 << 20:
        raise ValueError("source download exceeds 8 MiB")
    return raw

def main():
    records, archives = [], []
    for population, (repo, commit) in PINS.items():
        base = f"https://raw.githubusercontent.com/{repo}/{commit}/"
        license_raw = fetch(base + "LICENSE")
        if not license_raw.startswith(b"                                 Apache License") and not license_raw.lstrip().startswith(b"Apache License"):
            raise ValueError("pinned license is not the reviewed Apache license")
        if b"Version 2.0, January 2004" not in license_raw:
            raise ValueError("pinned Apache version absent")
        archive_url = f"https://codeload.github.com/{repo}/tar.gz/{commit}"
        raw = fetch(archive_url)
        archives.append({"repository": repo, "commit": commit, "url": archive_url, "bytes": len(raw), "digest": digest(raw), "license_url": base + "LICENSE", "license_digest": digest(license_raw)})
        with tarfile.open(fileobj=io.BytesIO(raw), mode="r:gz") as archive:
            for member in archive.getmembers():
                path = member.name.split("/", 1)[-1]
                selected = path in {"LICENSE", "NOTICE"}
                if population == "public":
                    selected |= path in APP | GENERATED or path.startswith("thrift/") and path.endswith(".go") and not path.endswith("_test.go")
                else:
                    selected |= path.startswith("thrift/") and path.endswith(".thrift")
                if not selected or not member.isfile():
                    continue
                if member.size > 4 << 20 or any(part in ("", ".", "..") for part in Path(path).parts):
                    raise ValueError("unsafe source member")
                content = archive.extractfile(member).read()
                target = ROOT / "testdata" / population / path
                target.parent.mkdir(parents=True, exist_ok=True)
                if target.exists() and target.read_bytes() != content:
                    raise ValueError("refuse to replace frozen source " + str(target))
                target.write_bytes(content)
                records.append({"population": population, "path": path, "bytes": len(content), "digest": digest(content), "origin": "upstream-unchanged", "repository": repo, "commit": commit, "url": base + path, "role": "application" if path in APP else "generated" if path in GENERATED else "license" if path in {"LICENSE", "NOTICE"} else "input-context"})
    module = b"module github.com/uber/jaeger-client-go\n\ngo 1.26.5\n\nrequire github.com/opentracing/opentracing-go v1.2.0\n"
    target = ROOT / "testdata/public/go.mod"
    if target.exists() and target.read_bytes() != module:
        raise ValueError("refuse to replace evaluator module")
    target.write_bytes(module)
    records.append({"population": "public", "path": "go.mod", "bytes": len(module), "digest": digest(module), "origin": "evaluator-authored", "role": "selected-module-context", "note": "Upstream predates go.mod. Exact selected unchanged source set is evaluated as a derivative module; no original-upstream SCIP commit claim."})
    result = {"schema": "t521-source-input-manifest-v1", "record_date": "2026-10-18", "archives": archives, "files": sorted(records, key=lambda r: (r["population"], r["path"]))}
    (ROOT / "source-inputs.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"files": len(records), "bytes": sum(r["bytes"] for r in records), "archives_bytes": sum(r["bytes"] for r in archives)}))

if __name__ == "__main__":
    main()
