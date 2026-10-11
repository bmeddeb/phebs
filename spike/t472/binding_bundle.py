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
try:
    from . import frames
except ImportError:
    import frames

BUNDLE_SCHEMA = "t472-preregistration-bundle-v2"
_SHA_RE = re.compile(r"\Asha256:[0-9a-f]{64}\Z")

MACHINERY = {
    "label_protocol": "spike/t111/label_protocol.py",
    "pilot_harness": "pilot/validation/harness.py",
    "enumerate_universe": "spike/t472/enumerate_universe.py",
    "frames": "spike/t472/frames.py",
    "caller_scoring": "spike/t472/caller_scoring.py",
    "go_source_parser": "spike/t472/gosites/main.go",
    "binding_bundle": "spike/t472/binding_bundle.py",
}


class BundleError(ValueError):
    """A bundle binding is neither a sha256 digest nor an explicit missing entry."""


def sha256_file(path: Path) -> str:
    return protocol.sha256_bytes(path.read_bytes())


def build_bundle(repo_root: Path, frames_dir: Path) -> dict:
    spike = repo_root / "spike" / "t472"
    plan = json.loads((frames_dir / "frames.plan.json").read_text())
    if plan.get("schema") != "t472-frames-plan-v2":
        raise BundleError("frames plan is not t472-frames-plan-v2")
    lock = json.loads((spike / "corpus.lock.json").read_text())
    universe = frames.load_universe(spike / "universe")
    scanner = universe["summary"]["source_scanner"]
    if scanner["path"] != MACHINERY["go_source_parser"] or scanner["sha256"] != sha256_file(repo_root / scanner["path"]):
        raise BundleError("source parser provenance is stale")
    expected_provenance = {
        "universe_summary_sha256": universe["summary_sha256"],
        "universe_declarations_sha256": universe["declarations_sha256"],
        "universe_sites": universe["site_files"],
        "parameters_sha256": sha256_file(spike / "validation.parameters.json"),
        "source_census_sha256": sha256_file(spike / "source.census.json"),
        "corpus_lock_sha256": sha256_file(spike / "corpus.lock.json"),
        "enumerate_universe_sha256": sha256_file(spike / "enumerate_universe.py"),
        "frames_py_sha256": sha256_file(spike / "frames.py"),
    }
    if plan["provenance"] != expected_provenance or plan["missing_bindings"] != frames.MISSING_BINDINGS:
        raise BundleError("frames provenance is stale or incomplete")
    commits = sorted(r["derived"]["derived_commit"] for r in lock["repos"] if r["derived"].get("corpus_admitted"))
    if plan["corpus_derived_commits"] != commits or sorted(r["derived_commit"] for r in universe["summary"]["repositories"]) != commits:
        raise BundleError("universe or frame commits disagree with the admitted lock")
    members = {}
    for f in plan["frames"]:
        if "file" in f:
            if Path(f["file"]).name != f["file"] or sha256_file(frames_dir / f["file"]) != f["sha256"]:
                raise BundleError("frame member identity mismatch")
            members[f["file"]] = {"sha256": f["sha256"], "rows": f["rows"]}
    members["frames.excluded.json"] = {"sha256": sha256_file(frames_dir / "frames.excluded.json")}
    frozen = {"label_protocol": "sha256:bcafdee6cab0b4ddd98ddbd56896546a47abe2c164be955ace28542686f329ee",
              "pilot_harness": "sha256:3a8dcc9ecb26588dcb1d53c32761fe15fdd964b592cdc30fdac09fd4d9ce3b69"}
    if any(sha256_file(repo_root / MACHINERY[name]) != digest for name, digest in frozen.items()):
        raise BundleError("frozen machinery changed")

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
                        "frames": [f["frame_id"] for f in plan["frames"]], "members": members},
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
                 "schema": "t472-nist-pulse-reference-v1", "status": "skeleton_local_only",
                 "fields": {"pulse": None, "output_value": None, "pulse_timestamp": None}},
            ],
        },
        "quality_scored": False,
        "predictions_disclosed": False,
    }
    return bundle


def validate_bundle(bundle: dict) -> list[str]:
    """Validate required preparation structure and every digest; report named nulls."""
    def fields(value, required, trail):
        if not isinstance(value, dict) or set(value) != set(required):
            raise BundleError(f"{trail}: missing or unknown required fields")

    top = {"schema", "protocol_doc", "parameters", "source_census", "corpus_lock", "universe",
           "frames_plan", "machinery", "frozen_machinery_unedited", "label_commitment_schema",
           "harness_schema", "corpus_derived_commits", "candidate_identities",
           "randomness_commitment_sequence", "external_publication", "quality_scored", "predictions_disclosed"}
    fields({k: v for k, v in bundle.items() if k != "missing_bindings"}, top, "bundle")
    if bundle["schema"] != BUNDLE_SCHEMA or bundle["label_commitment_schema"] != protocol.LABEL_COMMITMENT_SCHEMA \
            or bundle["harness_schema"] != pilot_harness.HARNESS_SCHEMA:
        raise BundleError("bundle schema or frozen machinery schema mismatch")
    if bundle["frozen_machinery_unedited"] is not True or bundle["quality_scored"] is not False \
            or bundle["predictions_disclosed"] is not False:
        raise BundleError("preparation bundle has inconsistent authority flags")
    for key, path in (("protocol_doc", "docs/CALLER_QUALITY_PROTOCOL.md"),
                      ("source_census", "spike/t472/source.census.json"),
                      ("corpus_lock", "spike/t472/corpus.lock.json")):
        fields(bundle[key], {"path", "sha256"}, key)
        if bundle[key]["path"] != path:
            raise BundleError("known preparation path changed")
    fields(bundle["parameters"], {"path", "sha256", "status"}, "parameters")
    if bundle["parameters"]["status"] != "approved_not_sealed" or bundle["parameters"]["path"] != "spike/t472/validation.parameters.json":
        raise BundleError("preparation parameters must remain approved_not_sealed")
    fields(bundle["universe"], {"dir", "summary_sha256", "sites", "declarations_sha256"}, "universe")
    if bundle["universe"]["dir"] != "spike/t472/universe":
        raise BundleError("universe directory changed")
    if not isinstance(bundle["universe"]["sites"], dict) or not bundle["universe"]["sites"]:
        raise BundleError("universe site members are missing")
    for name, member in bundle["universe"]["sites"].items():
        if Path(name).name != name:
            raise BundleError("universe member path is invalid")
        fields(member, {"file", "rows", "sha256"}, name)
        if member["file"] != name or type(member["rows"]) is not int or member["rows"] < 0:
            raise BundleError("universe member metadata is invalid")
    fields(bundle["frames_plan"], {"path", "sha256", "frames", "members"}, "frames_plan")
    if bundle["frames_plan"]["frames"] != ["recall", "attribution", "precision", "abstention", "unresolved"]:
        raise BundleError("required frames are missing or reordered")
    if bundle["frames_plan"]["path"] != "spike/t472/frames/frames.plan.json":
        raise BundleError("frames plan path changed")
    frame_members = bundle["frames_plan"]["members"]
    fields(frame_members, {"frames.recall.jsonl.gz", "frames.attribution.jsonl", "frames.excluded.json"}, "frame members")
    for name, member in frame_members.items():
        fields(member, {"sha256"} if name == "frames.excluded.json" else {"sha256", "rows"}, name)
        if name != "frames.excluded.json" and (type(member["rows"]) is not int or member["rows"] < 0):
            raise BundleError("frame row count is invalid")
    fields(bundle["machinery"], MACHINERY, "machinery")
    for name, rel in MACHINERY.items():
        fields(bundle["machinery"][name], {"path", "sha256"}, name)
        if bundle["machinery"][name]["path"] != rel:
            raise BundleError("machinery path changed")
    fields(bundle["candidate_identities"], frames.MISSING_BINDINGS, "candidate identities")
    for value in bundle["candidate_identities"].values():
        if value is not None:
            fields(value, {"identity", "sha256"}, "candidate binding")
            if not isinstance(value["identity"], str) or not value["identity"].strip():
                raise BundleError("candidate identity is invalid")
    commits = bundle["corpus_derived_commits"]
    if not isinstance(commits, list) or not commits or commits != sorted(set(commits)) \
            or any(not isinstance(c, str) or not re.fullmatch(r"[0-9a-f]{40}", c) for c in commits):
        raise BundleError("corpus commits are invalid")
    steps = bundle["randomness_commitment_sequence"]
    if not isinstance(steps, list) or len(steps) != 5:
        raise BundleError("randomness commitment sequence is incomplete")
    for index, step in enumerate(steps, 1):
        fields(step, {"step", "action", "binding"}, "randomness step")
        if step["step"] != index or not isinstance(step["action"], str) or not step["action"]:
            raise BundleError("randomness step is invalid")
        if step["binding"] is not None and (not isinstance(step["binding"], str) or not _SHA_RE.fullmatch(step["binding"])):
            raise BundleError("randomness binding must be a digest or an explicit missing input")
    publication = bundle["external_publication"]
    fields(publication, {"published", "prepared_payloads"}, "external publication")
    if publication["published"] is not False or len(publication["prepared_payloads"]) != 2:
        raise BundleError("preparation publication must remain local")
    for payload, (kind, schema, expected) in zip(publication["prepared_payloads"],
            [("github_gist_label_commitment", protocol.LABEL_COMMITMENT_SCHEMA, protocol.LABEL_COMMITMENT_FIELDS),
             ("nist_beacon_reference", "t472-nist-pulse-reference-v1", {"pulse", "output_value", "pulse_timestamp"})]):
        fields(payload, {"kind", "schema", "status", "fields"}, "publication payload")
        fields(payload["fields"], expected, "publication fields")
        if (payload["kind"], payload["schema"], payload["status"]) != (kind, schema, "skeleton_local_only"):
            raise BundleError("publication skeleton identity is invalid")
    missing = []

    def walk(value, trail, key=""):
        if value is None:
            missing.append(trail)
        elif key == "sha256" or key.endswith("_sha256"):
            if not isinstance(value, str) or not _SHA_RE.fullmatch(value):
                raise BundleError(f"{trail}: malformed or absent required digest")
        elif isinstance(value, dict):
            for name, item in value.items():
                walk(item, f"{trail}.{name}", name)
        elif isinstance(value, list):
            for i, item in enumerate(value):
                walk(item, f"{trail}[{i}]")
        elif not isinstance(value, (str, bool, int)):
            raise BundleError(f"{trail}: unsupported binding value")

    # Null skeleton fields are expressly missing; known digest records must never be null.
    known = {k: v for k, v in bundle.items() if k not in {"missing_bindings", "external_publication", "candidate_identities", "randomness_commitment_sequence"}}
    walk(known, "bundle")
    if missing:
        raise BundleError("known preparation binding is null")
    for key in ("candidate_identities", "randomness_commitment_sequence", "external_publication"):
        walk(bundle[key], "bundle." + key)
    missing.sort()
    if "missing_bindings" in bundle and bundle["missing_bindings"] != missing:
        raise BundleError("recorded missing-binding list is stale")
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
