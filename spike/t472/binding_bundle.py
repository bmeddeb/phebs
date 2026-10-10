"""T47.2b preregistration binding bundle (protocol section 10).

Assembles one bundle object binding every already-known identity by sha256
and naming every not-yet-known binding explicitly. Nothing is sealed, drawn,
published or scored here; external-publication payloads exist only as local
unpublished skeletons whose null fields are filled at sealing time.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

_SPIKE_DIR = Path(__file__).resolve().parent
for _extra in ("../t111", "../../pilot/validation"):
    p = (_SPIKE_DIR / _extra).resolve()
    if str(p) not in sys.path:
        sys.path.insert(0, str(p))

import label_protocol as protocol  # noqa: E402
import harness as pilot_harness  # noqa: E402

BUNDLE_SCHEMA = "t472-preregistration-bundle-v1"
_SHA_RE = re.compile(r"\Asha256:[0-9a-f]{64}\Z")

MACHINERY = {
    "label_protocol": "spike/t111/label_protocol.py",
    "pilot_harness": "pilot/validation/harness.py",
    "enumerate_universe": "spike/t472/enumerate_universe.py",
    "frames": "spike/t472/frames.py",
    "caller_scoring": "spike/t472/caller_scoring.py",
}


class BundleError(ValueError):
    """A bundle binding is neither a sha256 digest nor an explicit missing entry."""


def sha256_file(path: Path) -> str:
    return protocol.sha256_bytes(path.read_bytes())


def build_bundle(repo_root: Path, frames_dir: Path) -> dict:
    spike = repo_root / "spike" / "t472"
    plan = json.loads((frames_dir / "frames.plan.json").read_text())
    if plan.get("schema") != "t472-frames-plan-v1":
        raise BundleError("frames plan is not t472-frames-plan-v1")
    lock = json.loads((spike / "corpus.lock.json").read_text())

    bundle = {
        "schema": BUNDLE_SCHEMA,
        "protocol_doc": {"path": "docs/CALLER_QUALITY_PROTOCOL.md",
                         **{"sha256": sha256_file(repo_root / "docs" / "CALLER_QUALITY_PROTOCOL.md")}},
        "parameters": {"path": "spike/t472/validation.parameters.json",
                       "sha256": sha256_file(spike / "validation.parameters.json"),
                       "status": "approved_not_sealed"},
        "source_census": {"path": "spike/t472/source.census.json",
                          "sha256": sha256_file(spike / "source.census.json")},
        "corpus_lock": {"path": "spike/t472/corpus.lock.json",
                        "sha256": sha256_file(spike / "corpus.lock.json")},
        "universe": {"dir": "spike/t472/universe",
                     "summary_sha256": plan["provenance"]["universe_summary_sha256"],
                     "sites": plan["provenance"]["universe_sites"],
                     "declarations_sha256": plan["provenance"]["universe_declarations_sha256"]},
        "frames_plan": {"path": str(frames_dir.relative_to(repo_root)) + "/frames.plan.json",
                        "sha256": sha256_file(frames_dir / "frames.plan.json"),
                        "frames": [f["frame_id"] for f in plan["frames"]]},
        "machinery": {name: {"path": rel, "sha256": sha256_file(repo_root / rel)}
                      for name, rel in MACHINERY.items()},
        "frozen_machinery_unedited": True,
        "label_commitment_schema": protocol.LABEL_COMMITMENT_SCHEMA,
        "harness_schema": pilot_harness.HARNESS_SCHEMA,
        "corpus_derived_commits": plan["corpus_derived_commits"],
        "candidate_identities": {binding: None for binding in plan["missing_bindings"]},
        "randomness_commitment_sequence": [
            {"step": 1, "action": "record NIST beacon pulse", "binding": None},
            {"step": 2, "action": "derive 64-hex seed from pulse", "binding": None},
            {"step": 3, "action": "draw per-stratum samples from sealed frames plan", "binding": None},
            {"step": 4, "action": "Ben freezes blind labels (t111 freeze_labels, O_EXCL)",
             "binding": None},
            {"step": 5, "action": "build label commitment (t111 schema) and publish receipt",
             "binding": None},
        ],
        "external_publication": {
            "published": False,
            "prepared_payloads": [
                {"kind": "github_gist_label_commitment",
                 "schema": protocol.LABEL_COMMITMENT_SCHEMA, "status": "skeleton_local_only",
                 "fields": {k: None for k in sorted(protocol.LABEL_COMMITMENT_FIELDS)}},
                {"kind": "nist_beacon_reference",
                 "schema": protocol.GITHUB_GIST_RECEIPT_SCHEMA, "status": "skeleton_local_only",
                 "fields": {"pulse": None, "output_value": None, "pulse_timestamp": None}},
            ],
        },
        "quality_scored": False,
        "predictions_disclosed": False,
    }
    return bundle


def validate_bundle(bundle: dict) -> list[str]:
    """Fail-closed completeness walk: every binding is a digest or None.

    Returns the list of still-missing binding names; raises on malformed
    values so a partially-filled or corrupted bundle cannot pass as ready.
    """
    missing: list[str] = []

    def walk(value: object, trail: str) -> None:
        if isinstance(value, dict):
            if "sha256" in value:
                if not _SHA_RE.match(value["sha256"]):
                    raise BundleError(f"{trail}: malformed sha256 {value['sha256']!r}")
                return
            for key, item in value.items():
                walk(item, f"{trail}.{key}")
        elif isinstance(value, list):
            for i, item in enumerate(value):
                walk(item, f"{trail}[{i}]")
        elif value is None:
            missing.append(trail)
        elif isinstance(value, str) and value == "approved_not_sealed":
            pass
        elif isinstance(value, (str, bool, int)):
            pass
        else:
            raise BundleError(f"{trail}: unexpected binding value {value!r}")

    walk(bundle, "bundle")
    return missing


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--repo-root", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--frames-dir", type=Path, default=None)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    frames_dir = args.frames_dir or args.repo_root / "spike" / "t472" / "frames"
    bundle = build_bundle(args.repo_root, frames_dir)
    missing = validate_bundle(bundle)
    bundle["missing_bindings"] = missing
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(bundle, indent=1, sort_keys=True) + "\n")
    print(f"{args.out}: bindings complete, missing={len(missing)}")
    for name in missing:
        print(f"  MISSING {name}")


if __name__ == "__main__":
    main()
