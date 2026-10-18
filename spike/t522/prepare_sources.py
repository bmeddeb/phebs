#!/usr/bin/env python3
"""Source/license preparation only: no compiler, SCIP reader or extractor.

Unchanged public source pins are provenance, not evaluator Git identities.
All license bytes are fetched and checked before any application source read.
Existing retained files may be verified but are never silently replaced.
"""
import hashlib
import json
from pathlib import Path
import urllib.request

ROOT = Path(__file__).resolve().parent
CADENCE = "98f3d45ec820380dadae38206753777a7417709f"
IDL = "ee96c0aebffaea083bdeab66b62c7e18291045f3"


def sha(raw):
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def retain(path, raw):
    target = ROOT / path
    target.parent.mkdir(parents=True, exist_ok=True)
    if target.exists():
        assert target.read_bytes() == raw, "refusing to replace " + path
    else:
        with target.open("xb") as stream:
            stream.write(raw)
    return {"path": path, "bytes": len(raw), "digest": sha(raw)}


def fetch(url, limit):
    with urllib.request.urlopen(url, timeout=20) as response:
        raw = response.read(limit + 1)
    assert len(raw) <= limit, "source byte bound"
    return raw


def main():
    licenses = [
        ("cadence", "Apache-2.0", f"https://raw.githubusercontent.com/uber/cadence/{CADENCE}/LICENSE"),
        ("cadence-idl", "Apache-2.0", f"https://raw.githubusercontent.com/cadence-workflow/cadence-idl/{IDL}/LICENSE"),
        ("thriftrw-go", "MIT", "https://raw.githubusercontent.com/thriftrw/thriftrw-go/v1.34.0/LICENSE"),
        ("multierr", "MIT", "https://raw.githubusercontent.com/uber-go/multierr/v1.11.0/LICENSE.txt"),
        ("zap", "MIT", "https://raw.githubusercontent.com/uber-go/zap/v1.28.0/LICENSE"),
    ]
    admitted = []
    for name, kind, url in licenses:
        raw = fetch(url, 32768)
        marker = b"Apache License" if kind == "Apache-2.0" else b"Permission is hereby granted, free of charge"
        assert marker in raw, "unrecognized permissive license"
        admitted.append(dict(name=name, license=kind, url=url, **retain("licenses/" + name + ".LICENSE", raw)))
    license_record = dict(schema="t522-license-admission-v1", record_date="2026-10-18", application_reads_before_license_verification=0, licenses=admitted,
                          selected_source_notice="Cadence repository is Apache-2.0; selected handwritten files additionally retain their in-file MIT grant. Original notices and generated headers are unchanged.",
                          dependency_scope="Cached runtime modules, not scored source or generator execution; retain direct MIT notices. No proprietary/source-available application input is admitted.")
    retain("licenses/record.json", (json.dumps(license_record, indent=2) + "\n").encode())

    files = []
    for path in ("LICENSE", "NOTICE", "common/types/mapper/thrift/health.go", "common/types/health.go", ".gen/go/health/health.go"):
        url = f"https://raw.githubusercontent.com/uber/cadence/{CADENCE}/{path}"
        raw = fetch(url, 4 << 20)
        files.append(dict(population="public", repository="uber/cadence", upstream_commit=CADENCE, source_path=path, url=url,
                          authored=False, **retain("testdata/public/" + path, raw)))
    for path in ("LICENSE", "thrift/health.thrift"):
        url = f"https://raw.githubusercontent.com/cadence-workflow/cadence-idl/{IDL}/{path}"
        raw = fetch(url, 65536)
        files.append(dict(population="idl", repository="cadence-workflow/cadence-idl", upstream_commit=IDL, source_path=path, url=url,
                          authored=False, **retain("testdata/idl/" + path, raw)))
    module = b"module github.com/uber/cadence\n\ngo 1.26.5\n\nrequire (\n\tgo.uber.org/thriftrw v1.34.0\n\tgo.uber.org/multierr v1.11.0\n\tgo.uber.org/zap v1.28.0\n)\n"
    files.append(dict(population="public", source_path="go.mod", authored=True, purpose="Selected unchanged source module with cached dependencies; not the full upstream build context and no application source stub", **retain("testdata/public/go.mod", module)))
    record = dict(schema="t522-source-inputs-v1", record_date="2026-10-18", candidate_runs=0, indexer_runs=0,
                  source_context="Only selected unchanged health mapper/types/generated files; no full Cadence application claim. Evaluator module bytes are explicit. Upstream Git pins are source provenance only; lead will create evaluator source/index commits after any reviewed offline index preparation.",
                  corpus_selection="Actual bidirectional application mapper chosen before indexing or candidate output; same-named internal fields remain negatives.",
                  gitlink=dict(repository="uber/cadence", commit=CADENCE, path="idls", target=IDL, authority="Existing pinned T221 source metadata; independent Raw/SHA1/source witnesses are frozen before candidate execution, no T221/T222 result transfer"),
                  files=files)
    retain("source-inputs.json", (json.dumps(record, indent=2) + "\n").encode())
    print(json.dumps({"source_files":len(files), "bytes":sum(f["bytes"] for f in files), "source_inputs_digest":sha((ROOT/"source-inputs.json").read_bytes()), "candidate_runs":0, "indexer_runs":0}))


if __name__ == "__main__":
    main()
