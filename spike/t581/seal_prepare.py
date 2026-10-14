#!/usr/bin/env python3
"""T58.1: prepare the §7 seal inputs, the gist text, and the seal record.

Digests are computed, never transcribed, so the seal cannot drift from the bytes it
claims to bind. Three modes:

    seal_prepare.py                      write round/seal-inputs.json and round/SEAL.md
    seal_prepare.py --gist URL --sealed-utc YYYY-MM-DD
                                         additionally write round/seal.json

`seal.json` is the file `export/main.go` validates before it will touch the
population, so this tool refuses to write one whose gist is not a real published
URL — the same shape check the exporter applies. Publishing the gist itself is
Ben's step under §7; nothing here publishes anything.

The protocol digest is computed over the file as it stands when this runs, so the
protocol text must be final first: editing it afterwards invalidates the seal.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SEAL_SCHEMA = "t581-seal-v1"
INPUTS_SCHEMA = "t581-seal-inputs-v1"
GIST_RE = re.compile(r"^https://gist\.github\.com/[A-Za-z0-9_-]+/[0-9a-f]{16,64}$")
DATE_RE = re.compile(r"^\d{4}-\d{2}-\d{2}$")

PROTOCOL = "docs/GRPC_CONSUMER_QUALITY_PROTOCOL.md"

# Every artifact §7 requires the seal to bind.
BOUND = (
    "spike/t581/corpus.lock.json",
    "spike/t581/population/units.jsonl.gz",
    "spike/t581/population/population.json",
    "spike/t581/round/sites.jsonl.gz",
    "spike/t581/lock_corpus.py",
    "spike/t581/enumerate_population.py",
    "spike/t581/export/main.go",
    "spike/t581/score.py",
    "spike/t581/test_score.py",
    "spike/t581/oracle/main.go",
    "spike/t581/oracle/main_test.go",
    "spike/t581/seal_prepare.py",
)

# The §6 thresholds, frozen by Ben 2026-10-14 before any oracle pass. Restated
# here so the gist carries them without a reader having to open the protocol.
THRESHOLDS = {
    "coverage_min": 0.99,
    "state_agreement_min": 0.99,
    "inversions_max": 0,
    "call_registration_swaps_max": 0,
    "spurious_per_scored_site_max": 0.01,
    "pending_after_adjudication_max": 0,
    "scored_sites_min": 300,
    "production_role_agreement_min": 0.99,
    "disclosed_not_scored": [
        "per_repository_scored_counts_with_concentration",
        "test_generated_mock_vendor_role_agreement",
        "out_of_domain_candidate_counts",
    ],
}


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            digest.update(block)
    return "sha256:" + digest.hexdigest()


def git(*args: str) -> str:
    return subprocess.run(
        ["git", "-C", str(ROOT), *args], check=True, capture_output=True, text=True,
    ).stdout.strip()


def build_inputs() -> dict:
    protocol = ROOT / PROTOCOL
    if not protocol.is_file():
        raise SystemExit(f"missing protocol: {protocol}")
    artifacts = {PROTOCOL: {"sha256": sha256(protocol), "bytes": protocol.stat().st_size}}
    for relative in BOUND:
        path = ROOT / relative
        if not path.is_file():
            raise SystemExit(f"missing bound artifact: {relative}")
        artifacts[relative] = {"sha256": sha256(path), "bytes": path.stat().st_size}

    head = git("rev-parse", "HEAD")
    dirty = git("status", "--porcelain", "--", *BOUND, PROTOCOL)
    return {
        "schema": INPUTS_SCHEMA,
        "protocol": "docs/GRPC_CONSUMER_QUALITY_PROTOCOL.md",
        "prepared_utc": __import__("datetime").datetime.now(
            __import__("datetime").timezone.utc
        ).strftime("%Y-%m-%d"),
        "commit": head,
        "commit_subject": git("log", "-1", "--format=%s", head),
        "bound_artifacts_clean": not dirty,
        "dirty_paths": [line[3:] for line in dirty.splitlines()] if dirty else [],
        "units_sha256": artifacts["spike/t581/population/units.jsonl.gz"]["sha256"],
        "protocol_sha256": artifacts[PROTOCOL]["sha256"],
        "thresholds_frozen_by": "Ben, 2026-10-14, before any oracle pass",
        "thresholds": THRESHOLDS,
        "presel_pass": {
            "scored_sites": 34089,
            "floor": 300,
            "out_of_domain_candidates": 2870212,
            "scored_to_out_of_domain": "1 : 84.2",
            "units": 13,
            "aggregates": 13,
            "run_seconds": 8.6,
        },
        "disclosed_breach": {
            "occurred_utc": "2026-10-14",
            "summary": (
                "The exporter ran over the locked population before any seal existed "
                "because a placeholder gist URL cleared a prefix-and-length guard; "
                "SIGPIPE stopped it after two rows and no candidate file was written"
            ),
            "disposition": "Ben elected to seal with the breach disclosed (2026-10-14)",
            "lead_recommendation": "interested — the lead caused the breach",
            "protocol_section": "§7 Recorded breach of the pre-seal prohibition",
        },
        "artifacts": artifacts,
    }


def gist_text(inputs: dict) -> str:
    lines = [
        "# T58.1 seal — Go gRPC syntactic consumer/registration site-coverage round",
        "",
        f"Protocol: `{inputs['protocol']}` at commit `{inputs['commit']}`",
        f"Commit subject: {inputs['commit_subject']}",
        "",
        "Published under §7 before `export` runs on the population. This gist is the",
        "seal; the thresholds and every digest below were frozen before any extractor",
        "run over the population.",
        "",
        "## Disclosed breach",
        "",
        inputs["disclosed_breach"]["summary"] + ".",
        "",
        f"Disposition: {inputs['disclosed_breach']['disposition']}.",
        f"Lead recommendation: {inputs['disclosed_breach']['lead_recommendation']}.",
        "See protocol §7 for what was and was not compromised.",
        "",
        "## Pre-seal oracle pass (no extractor)",
        "",
        f"- scored sites: **{inputs['presel_pass']['scored_sites']}** "
        f"(floor {inputs['presel_pass']['floor']})",
        f"- out-of-domain candidates: {inputs['presel_pass']['out_of_domain_candidates']} "
        f"({inputs['presel_pass']['scored_to_out_of_domain']})",
        f"- units {inputs['presel_pass']['units']}, aggregates "
        f"{inputs['presel_pass']['aggregates']}, "
        f"{inputs['presel_pass']['run_seconds']}s",
        "",
        "## Frozen thresholds (§6, Ben 2026-10-14, before any oracle pass)",
        "",
        "| Check | Bar |",
        "|---|---|",
    ]
    labels = {
        "coverage_min": "Coverage: scored sites with exactly one row",
        "state_agreement_min": "State agreement on covered sites",
        "inversions_max": "Resolved/ambiguous inversions",
        "call_registration_swaps_max": "Call/registration swaps (GD9)",
        "spurious_per_scored_site_max": "Confirmed spurious rows per scored site",
        "pending_after_adjudication_max": "Pending after adjudication",
        "scored_sites_min": "Scored sites",
        "production_role_agreement_min": "Production-role state agreement",
    }
    for key, label in labels.items():
        value = THRESHOLDS[key]
        suffix = "max" if key.endswith("_max") else "min"
        lines.append(f"| {label} | {'≤' if suffix == 'max' else '≥'} {value} |")
    lines += [
        "",
        "Disclosed but not scored: "
        + ", ".join(THRESHOLDS["disclosed_not_scored"])
        + ".",
        "",
        "## Bound digests",
        "",
        "| Artifact | sha256 | bytes |",
        "|---|---|---|",
    ]
    for name in [PROTOCOL, *BOUND]:
        entry = inputs["artifacts"][name]
        lines.append(f"| `{name}` | `{entry['sha256']}` | {entry['bytes']} |")
    lines += [
        "",
        f"Bound artifacts clean at commit: **{inputs['bound_artifacts_clean']}**",
    ]
    if inputs["dirty_paths"]:
        lines.append("Uncommitted changes in bound paths: " + ", ".join(inputs["dirty_paths"]))
    lines += [
        "",
        "## Claim boundary",
        "",
        "Site coverage only. Emitted operation and service identities are recorded but",
        "not scored, so a pass is not identity accuracy and T13.1's deferred ≥98%/≥90%",
        "bar is neither met nor addressed. The population is biased toward repositories",
        "vendoring modern split `*_grpc.pb.go` stubs in-tree; combined-style stubs,",
        "external-module contracts and generic instantiation calls are all silently out",
        "of domain. A pass says nothing about Go gRPC code outside that class.",
        "",
        "The oracle's independence is procedural, not technical: its author was",
        "instructed not to read the extractor, which cannot be enforced. See §3.",
        "",
    ]
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gist", help="published gist URL; required to write seal.json")
    parser.add_argument("--sealed-utc", help="seal date YYYY-MM-DD")
    parser.add_argument("--out-dir", type=Path, default=ROOT / "spike/t581/round")
    args = parser.parse_args()

    inputs = build_inputs()
    args.out_dir.mkdir(parents=True, exist_ok=True)

    inputs_path = args.out_dir / "seal-inputs.json"
    inputs_path.write_text(
        json.dumps(inputs, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    seal_md = args.out_dir / "SEAL.md"
    seal_md.write_text(gist_text(inputs), encoding="utf-8")
    print(f"wrote {inputs_path.relative_to(ROOT)}")
    print(f"wrote {seal_md.relative_to(ROOT)}  (gist-ready text)")

    if not inputs["bound_artifacts_clean"]:
        print(
            "\nWARNING: bound artifacts have uncommitted changes: "
            + ", ".join(inputs["dirty_paths"]),
            file=sys.stderr,
        )
        print(
            "Commit them before sealing, or the seal will bind digests no commit contains.",
            file=sys.stderr,
        )

    if not args.gist and not args.sealed_utc:
        print(
            "\nNo --gist given, so seal.json was NOT written. Publish round/SEAL.md as a "
            "gist, then re-run with --gist URL --sealed-utc YYYY-MM-DD."
        )
        return 0

    if not args.gist or not GIST_RE.match(args.gist):
        raise SystemExit(
            f"--gist must be a published https://gist.github.com/<user>/<hex-id> URL, got {args.gist!r}"
        )
    if not args.sealed_utc or not DATE_RE.match(args.sealed_utc):
        raise SystemExit(f"--sealed-utc must be YYYY-MM-DD, got {args.sealed_utc!r}")

    seal = {
        "schema": SEAL_SCHEMA,
        "units_sha256": inputs["units_sha256"],
        "protocol_sha256": inputs["protocol_sha256"],
        "sealed_utc": args.sealed_utc,
        "gist": args.gist,
    }
    seal_path = args.out_dir / "seal.json"
    seal_path.write_text(json.dumps(seal, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"\nwrote {seal_path.relative_to(ROOT)}")
    print("Commit it — the exporter also requires the seal to be tracked in git.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
