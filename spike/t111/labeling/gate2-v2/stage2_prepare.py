#!/usr/bin/env python3
"""GATE2-V2 Stage 2 — carry-forward and exact-power preparation glue.

Wires the sealed Stage-0 tools together for the run after Stage-1 heads
seal: burn-ledger coordinates → carry-forward mapper (burn on doubt) →
census-v2 seed burns → exact power against aggregate and per-fixture
cardinalities net of frame-specific burns. Fails closed at every step.

Pure glue: no thresholds, no sampling rules, no population knowledge.
Tested exclusively against synthetic fixtures until Stage 1 has sealed;
outside --synthetic it refuses to run unless a Stage-1 receipt exists,
matches the sealed receipt schema, and binds the sealed cutoff and query
digest.

NOTE: this tool postdates the sealed Stage-0 code-path inventory. Before
any sealed-mode execution it must be reviewed and recorded in a Stage-2
authorization entry (its own digest bound), per the status review of
2026-07-17.

Inputs:
  --ledger           burn-ledger JSON (cohorts[].coordinates[])
  --heads            JSON {fixture: {"old_commit","new_commit","repo_dir"}}
  --cardinalities    JSON {frame: {"population": N, "census": C}} from the
                     sealed frame-enumeration step (run separately)
  --frame-membership JSON {frame: ["system:path:start:end", ...]} from the
                     same enumeration step; a burned coordinate absent from
                     every frame's membership burns against ALL frames
                     (burn on doubt)
  --design           JSON with one {"p","threshold"} object for every
                     aggregate frame plus "per_fixture_precision"
  --receipt          Stage-1 receipt (schema/cutoff/query-digest bound);
                     required unless --synthetic
  --out              fresh output directory (refuses to overwrite)
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
from fractions import Fraction
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE / "stage0"))
sys.path.insert(0, str(HERE))
from carry_forward import classify  # noqa: E402

RECEIPT_SCHEMA = "t111-gate2-v2-stage1-receipt-v1"
SEALED_FIXTURES = ("dapr", "loki", "online-boutique", "temporal")
AGGREGATE_FRAMES = (
    "client_call_precision",
    "client_call_recall",
    "registration_precision",
    "registration_recall",
)
PRECISION_FRAMES = ("client_call_precision", "registration_precision")
PER_FIXTURE_DESIGN = "per_fixture_precision"

# The old side is not an operator choice.  It is the four-fixture projection
# of the last completed carry-forward receipt, which carried every disclosed
# coordinate to one common source snapshot before GATE2-V2 began.  The future
# authorization must bind this receipt and these exact values.
PRIOR_LINEAGE_PATH = HERE.parent / "expansion-lineage.json"
PRIOR_LINEAGE_FILE_SHA256 = (
    "sha256:9c821523db05bf959f9e01e05529d18bfede1f3c505d54019fb9c33d602dc430"
)
PRIOR_LINEAGE_BINDING = (
    "sha256:86d0a76a510ecd99e6be939132bf9efc46c96e0c314583fde2f9e6d72d865e16"
)
SEALED_LEDGER_SHA256 = (
    "sha256:4e6e2382361f1a0223562d4cbac921f39944ceb36c916912fa0ca5c259e3044a"
)
SEALED_OLD_COMMITS = {
    "dapr": "08aebd8b2effa2ed939ad5531e25ff8b21a36ef1",
    "loki": "1362d2770ee2abba5e130d67cf30bcc4eefa0da0",
    "online-boutique": "9a4616e77f0f9cbcbecaf27d711c38890dda1404",
    "temporal": "8224a5375112079ad905c4ea829420306431462c",
}
SEALED_REPO_DIRS = {
    fixture: str((HERE.parents[1] / "corpus" / fixture).resolve())
    for fixture in SEALED_FIXTURES
}
ALLOWED_GIT_ENV = {"GIT_PAGER"}


class Stage2Error(Exception):
    pass


def load_json(path: str):
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


def sha256_bytes(payload: bytes) -> str:
    return "sha256:" + hashlib.sha256(payload).hexdigest()


def verify_prior_lineage() -> dict:
    """Verify the receipt that fixes the only permitted old commits."""
    payload = PRIOR_LINEAGE_PATH.read_bytes()
    if sha256_bytes(payload) != PRIOR_LINEAGE_FILE_SHA256:
        raise Stage2Error("prior carry-forward receipt digest does not match the sealed rule")
    record = json.loads(payload)
    if record.get("schema") != "t111-gate2-expansion-lineage-v2":
        raise Stage2Error("prior carry-forward receipt schema does not match the sealed rule")
    binding_body = dict(record)
    binding_body.pop("source_lineage_binding", None)
    binding_payload = json.dumps(
        ["t111-gate2-expansion-lineage-binding-v2", binding_body],
        sort_keys=True,
        separators=(",", ":"),
    ).encode()
    if (
        record.get("source_lineage_binding") != PRIOR_LINEAGE_BINDING
        or sha256_bytes(binding_payload) != PRIOR_LINEAGE_BINDING
    ):
        raise Stage2Error("prior carry-forward receipt binding does not match the sealed rule")
    inputs = record.get("inputs")
    if not isinstance(inputs, dict) or inputs.get("burn_ledger_sha256") != SEALED_LEDGER_SHA256:
        raise Stage2Error("prior carry-forward receipt does not bind the sealed burn ledger")
    commits = record.get("commits")
    if not isinstance(commits, dict) or {
        fixture: commits.get(fixture) for fixture in SEALED_FIXTURES
    } != SEALED_OLD_COMMITS:
        raise Stage2Error("prior carry-forward receipt commits do not match the sealed rule")
    return record


def verify_sealed_ledger(path: str, lineage: dict) -> None:
    expected = lineage["inputs"]["burn_ledger_sha256"]
    if sha256_bytes(Path(path).read_bytes()) != expected:
        raise Stage2Error("burn ledger digest does not match the prior carry-forward receipt")


def verify_receipt(path: str) -> dict:
    """Bind the receipt to the sealed Stage-0 constants, not just a status."""
    import stage1_snapshot as s1
    query_bytes, constants = s1.load_sealed_inputs()
    receipt = load_json(path)
    if receipt.get("schema") != RECEIPT_SCHEMA:
        raise Stage2Error(f"receipt schema {receipt.get('schema')!r} != {RECEIPT_SCHEMA}")
    if receipt.get("status") != "ADMITTED":
        raise Stage2Error("Stage-1 receipt is not ADMITTED")
    if receipt.get("cutoff") != constants["proposed_cutoff"]:
        raise Stage2Error("receipt cutoff does not match the sealed cutoff")
    if receipt.get("query_sha256") != s1.sha256_bytes(query_bytes):
        raise Stage2Error("receipt query digest does not match the sealed query")
    receipt_heads = receipt.get("heads")
    if not receipt.get("response_sha256") or not isinstance(receipt_heads, dict):
        raise Stage2Error("receipt lacks response digest or sealed heads")
    if set(receipt_heads) != set(constants["fixtures"]):
        raise Stage2Error("receipt heads do not exactly match the sealed fixtures")
    for fixture, head in receipt_heads.items():
        oid = head.get("head_oid") if isinstance(head, dict) else None
        if not isinstance(oid, str) or not s1.OID_RE.fullmatch(oid):
            raise Stage2Error(f"receipt lacks a full sealed head oid for {fixture!r}")
    return receipt


def verify_heads(heads: dict, receipt: dict) -> None:
    """Bind every mapping target to the exact admitted Stage-1 snapshot.

    The receipt is the authority for the new side of every comparison.  An
    exact fixture-set match also prevents a stale, omitted, or invented input
    from changing which coordinates are mapped versus conservatively burned.
    """
    receipt_heads = receipt.get("heads")
    if not isinstance(heads, dict) or not isinstance(receipt_heads, dict):
        raise Stage2Error("heads input and receipt heads must be JSON objects")
    if set(receipt_heads) != set(SEALED_FIXTURES):
        raise Stage2Error("receipt fixtures do not match the sealed carry-forward rule")
    if set(heads) != set(receipt_heads):
        raise Stage2Error("heads fixtures do not exactly match the sealed Stage-1 receipt")
    for fixture, sealed in receipt_heads.items():
        if not isinstance(sealed, dict) or not sealed.get("head_oid"):
            raise Stage2Error(f"receipt lacks a sealed head oid for {fixture!r}")
        submitted = heads[fixture]
        if not isinstance(submitted, dict) or submitted.get("new_commit") != sealed["head_oid"]:
            raise Stage2Error(f"heads {fixture!r} new_commit does not match the sealed Stage-1 head")
        if submitted.get("old_commit") != SEALED_OLD_COMMITS[fixture]:
            raise Stage2Error(
                f"heads {fixture!r} old_commit does not match the sealed carry-forward rule"
            )


def verify_git_execution_context() -> None:
    """Make admission and the sealed mapper use one Git identity/context."""
    if shutil.which("git") != "/usr/bin/git":
        raise Stage2Error("Git on PATH does not match the sealed /usr/bin/git identity")
    overrides = sorted(
        name for name in os.environ
        if name.startswith("GIT_") and name not in ALLOWED_GIT_ENV
    )
    if overrides:
        raise Stage2Error("Git execution environment contains an unsealed override")


def _git_admission(repo: str, *args: str) -> subprocess.CompletedProcess:
    try:
        return subprocess.run(
            ["/usr/bin/git", "-C", repo, *args],
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
        )
    except OSError as exc:
        raise Stage2Error("sealed source-history verification could not execute Git") from exc


def verify_source_history(heads: dict) -> None:
    """Refuse missing, shallow, or non-ancestral old-side history."""
    for fixture in SEALED_FIXTURES:
        submitted = heads[fixture]
        repo = submitted.get("repo_dir")
        if repo != SEALED_REPO_DIRS[fixture]:
            raise Stage2Error(f"heads {fixture!r} repo_dir does not match the sealed mirror")
        repo_path = Path(repo)
        if repo_path.is_symlink() or not repo_path.is_dir():
            raise Stage2Error(f"heads {fixture!r} repo_dir is not a real directory")
        shallow = _git_admission(repo, "rev-parse", "--is-shallow-repository")
        if shallow.returncode != 0 or shallow.stdout.strip() != b"false":
            raise Stage2Error(f"heads {fixture!r} repository is shallow or unreadable")
        old_commit = submitted["old_commit"]
        new_commit = submitted["new_commit"]
        for side, commit in (("old", old_commit), ("new", new_commit)):
            present = _git_admission(repo, "cat-file", "-e", f"{commit}^{{commit}}")
            if present.returncode != 0:
                raise Stage2Error(f"heads {fixture!r} {side} commit object is unavailable")
        ancestor = _git_admission(
            repo, "merge-base", "--is-ancestor", old_commit, new_commit
        )
        if ancestor.returncode != 0:
            raise Stage2Error(f"heads {fixture!r} old_commit is not an ancestor of new_commit")


def ledger_coordinates(ledger: dict) -> list[dict]:
    out = []
    for cohort in ledger.get("cohorts", []):
        for c in cohort.get("coordinates", []):
            out.append({"system": c["system"], "path": c["path"],
                        "start_line": int(c["start_line"]),
                        "end_line": int(c["end_line"]),
                        "cohort_id": cohort.get("cohort_id", "unknown")})
    if not out:
        raise Stage2Error("burn ledger contains no coordinates")
    return out


def carry_forward(coords: list[dict], heads: dict) -> list[dict]:
    results = []
    for c in coords:
        head = heads.get(c["system"])
        if head is None:
            results.append({**c, "decision": "burn", "rule": "system-not-in-snapshot"})
            continue
        d = classify(head["repo_dir"], head["old_commit"], head["new_commit"],
                     c["path"], c["start_line"], c["end_line"])
        results.append({**c, "decision": d.decision, "rule": d.rule})
    return results


def coordinate_key(c: dict) -> str:
    return f"{c['system']}:{c['path']}:{c['start_line']}:{c['end_line']}"


def _membership_system(key: object) -> str:
    if not isinstance(key, str):
        raise Stage2Error("frame membership contains a non-string coordinate")
    parts = key.split(":")
    if len(parts) != 4:
        raise Stage2Error("frame membership contains an ambiguous sampling-site coordinate")
    system, path, start_text, end_text = parts
    if system not in SEALED_FIXTURES:
        raise Stage2Error("frame membership contains an unknown fixture")
    if (
        not path
        or path.startswith("/")
        or "\n" in path
        or "\r" in path
        or any(part in {"", ".", ".."} for part in path.split("/"))
    ):
        raise Stage2Error("frame membership contains an unsafe path")
    if (
        not start_text.isascii()
        or not start_text.isdecimal()
        or not end_text.isascii()
        or not end_text.isdecimal()
        or str(int(start_text)) != start_text
        or str(int(end_text)) != end_text
        or int(start_text) <= 0
        or start_text != end_text
    ):
        raise Stage2Error("frame membership is not a canonical line-granular coordinate")
    return system


def _fraction(value: object, label: str) -> Fraction:
    if not isinstance(value, str):
        raise Stage2Error(f"{label} must be a num/den string")
    parts = value.split("/")
    if (
        len(parts) != 2
        or not all(part.isascii() and part.isdecimal() for part in parts)
        or any(str(int(part)) != part for part in parts)
        or int(parts[1]) == 0
    ):
        raise Stage2Error(f"{label} must be a canonical num/den string")
    return Fraction(int(parts[0]), int(parts[1]))


def validate_power_inputs(cardinalities: dict, membership: dict,
                          design: dict) -> tuple[dict, dict[str, dict[str, int]]]:
    """Validate the exact enumeration/design envelope and derive fixture Ns."""
    expected_frames = set(AGGREGATE_FRAMES)
    if not isinstance(cardinalities, dict) or set(cardinalities) != expected_frames:
        raise Stage2Error("cardinalities do not contain the exact aggregate frame set")
    if not isinstance(membership, dict) or set(membership) != expected_frames:
        raise Stage2Error("frame membership does not contain the exact aggregate frame set")
    expected_design = expected_frames | {PER_FIXTURE_DESIGN}
    if not isinstance(design, dict) or set(design) != expected_design:
        raise Stage2Error("design does not contain the exact consumed power fields")

    parsed_design = {}
    for name in sorted(expected_design):
        entry = design[name]
        if not isinstance(entry, dict) or set(entry) != {"p", "threshold"}:
            raise Stage2Error(f"design {name!r} must contain exactly p and threshold")
        p = _fraction(entry["p"], f"design {name!r} p")
        threshold = _fraction(entry["threshold"], f"design {name!r} threshold")
        if not (Fraction(0) <= threshold < p <= Fraction(1)):
            raise Stage2Error(f"design {name!r} values are outside the power domain")
        parsed_design[name] = {"p": p, "threshold": threshold}

    fixture_populations = {
        frame: {fixture: 0 for fixture in SEALED_FIXTURES}
        for frame in PRECISION_FRAMES
    }
    for frame in AGGREGATE_FRAMES:
        card = cardinalities[frame]
        if not isinstance(card, dict) or set(card) != {"population", "census"}:
            raise Stage2Error(f"cardinality {frame!r} must contain exactly population and census")
        population, census = card["population"], card["census"]
        if (
            isinstance(population, bool)
            or not isinstance(population, int)
            or population < 0
            or isinstance(census, bool)
            or not isinstance(census, int)
            or census != 0
        ):
            raise Stage2Error(f"cardinality {frame!r} is outside the sealed enumeration contract")
        keys = membership[frame]
        if not isinstance(keys, list):
            raise Stage2Error(f"frame membership {frame!r} is not a unique coordinate list")
        systems = [_membership_system(key) for key in keys]
        if len(keys) != len(set(keys)):
            raise Stage2Error(f"frame membership {frame!r} is not a unique coordinate list")
        for system in systems:
            if frame in fixture_populations:
                fixture_populations[frame][system] += 1
        derived_sum = (
            sum(fixture_populations[frame].values())
            if frame in fixture_populations else len(keys)
        )
        if derived_sum != population or len(keys) != population:
            raise Stage2Error(
                f"frame {frame!r} derived fixture population sum does not match aggregate cardinality"
            )
    return parsed_design, fixture_populations


def burns_per_frame(burns: list[dict], membership: dict,
                    frames: list[str]) -> tuple[dict[str, int], dict, int]:
    """Frame-specific burn counts. A burned coordinate in no frame's
    membership burns against every frame (burn on doubt)."""
    member_keys = {f: set(membership.get(f, [])) for f in frames}
    counts = {f: 0 for f in frames}
    fixture_counts = {
        frame: {fixture: 0 for fixture in SEALED_FIXTURES}
        for frame in PRECISION_FRAMES
    }
    unmapped = 0
    for b in burns:
        system = b.get("system")
        if system not in SEALED_FIXTURES:
            raise Stage2Error("burn ledger contains a system outside the sealed fixture set")
        k = coordinate_key(b)
        hit = [f for f in frames if k in member_keys[f]]
        if hit:
            for f in hit:
                counts[f] += 1
                if f in fixture_counts:
                    fixture_counts[f][system] += 1
        else:
            unmapped += 1
            for f in frames:
                counts[f] += 1
            for f in fixture_counts:
                fixture_counts[f][system] += 1
    for frame in PRECISION_FRAMES:
        if sum(fixture_counts[frame].values()) != counts[frame]:
            raise Stage2Error("per-fixture burn sum does not match aggregate precision burns")
    return counts, fixture_counts, unmapped


def power(cardinalities: dict, frame_burns: dict[str, int], design: dict) -> dict:
    from power_advisory import minimal_n
    out = {}
    for frame, card in cardinalities.items():
        pop = card["population"] - frame_burns.get(frame, 0)
        census = min(card["census"], pop)
        if pop <= 0:
            out[frame] = {
                "population_net": pop,
                "census": 0,
                "burns_applied": frame_burns.get(frame, 0),
                "minimal_sample_size": None,
                "feasible": False,
                "reason": "population exhausted by burns",
            }
            continue
        n = minimal_n(pop, census, design[frame]["p"], design[frame]["threshold"])
        out[frame] = {"population_net": pop, "census": census,
                      "burns_applied": frame_burns.get(frame, 0),
                      "minimal_sample_size": n, "feasible": n is not None}
    return out


def per_fixture_power(fixture_populations: dict, fixture_burns: dict,
                      design: dict) -> dict:
    from power_advisory import minimal_n
    rule = design[PER_FIXTURE_DESIGN]
    out = {}
    for frame in PRECISION_FRAMES:
        frame_out = {}
        for fixture in SEALED_FIXTURES:
            burns = fixture_burns[frame][fixture]
            pop = fixture_populations[frame][fixture] - burns
            if pop <= 0:
                frame_out[fixture] = {
                    "population_net": pop,
                    "census": 0,
                    "burns_applied": burns,
                    "minimal_sample_size": None,
                    "feasible": False,
                    "reason": "fixture population exhausted by burns",
                }
                continue
            n = minimal_n(pop, 0, rule["p"], rule["threshold"])
            frame_out[fixture] = {
                "population_net": pop,
                "census": 0,
                "burns_applied": burns,
                "minimal_sample_size": n,
                "feasible": n is not None,
            }
        out[frame] = frame_out
    return out


def write_durable_temp(path: Path, payload: bytes) -> Path:
    """Write one complete output to a sibling temp file before publication."""
    tmp = path.with_name(f".{path.name}.tmp")
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        view = memoryview(payload)
        while view:
            written = os.write(fd, view)
            if written <= 0:
                raise OSError("short write while publishing Stage-2 output")
            view = view[written:]
        os.fsync(fd)
    finally:
        os.close(fd)
    return tmp


def fsync_dir(path: Path) -> None:
    dir_fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(dir_fd)
    finally:
        os.close(dir_fd)


def publish_outputs(out: Path, mapped: list[dict], result: dict) -> None:
    """Atomically publish the complete two-file output directory.

    Both files become visible together only when the fully fsync'd sibling
    staging directory is renamed into the fresh requested output path.
    """
    staging = out.with_name(f".{out.name}.tmp")
    staging.mkdir(mode=0o700)
    seed = "".join(json.dumps(m, sort_keys=True) + "\n" for m in mapped).encode()
    preparation = (json.dumps(result, indent=2, sort_keys=True) + "\n").encode()
    targets = ((staging / "census-v2-seed.jsonl", seed),
               (staging / "stage2-preparation.json", preparation))
    temps = [(write_durable_temp(path, payload), path) for path, payload in targets]
    for tmp, path in temps:
        os.rename(tmp, path)
    fsync_dir(staging)
    if out.exists():
        raise FileExistsError(out)
    os.rename(staging, out)
    fsync_dir(out.parent)


def run(a) -> int:
    out = Path(a.out)
    if out.exists():
        print(f"stage2: refusing — output {out} already exists", file=sys.stderr)
        return 3

    receipt = None
    lineage = None
    if not a.synthetic:
        if not a.receipt:
            raise Stage2Error("no Stage-1 receipt and not --synthetic")
        receipt = verify_receipt(a.receipt)
        lineage = verify_prior_lineage()

    heads = load_json(a.heads)
    if receipt is not None:
        verify_heads(heads, receipt)
        verify_git_execution_context()
        verify_source_history(heads)
        verify_sealed_ledger(a.ledger, lineage)

    cards = load_json(a.cardinalities)
    design = load_json(a.design)
    membership = load_json(a.frame_membership)
    parsed_design, fixture_populations = validate_power_inputs(cards, membership, design)
    coords = ledger_coordinates(load_json(a.ledger))
    mapped = carry_forward(coords, heads)
    burns = [m for m in mapped if m["decision"] == "burn"]
    frames = sorted(cards)
    frame_burns, fixture_burns, unmapped = burns_per_frame(burns, membership, frames)
    for frame in PRECISION_FRAMES:
        aggregate_net = cards[frame]["population"] - frame_burns[frame]
        fixture_net = sum(
            fixture_populations[frame][fixture] - fixture_burns[frame][fixture]
            for fixture in SEALED_FIXTURES
        )
        if fixture_net != aggregate_net:
            raise Stage2Error("per-fixture net population sum does not match aggregate precision")
    p = power(cards, frame_burns, parsed_design)
    fixture_p = per_fixture_power(fixture_populations, fixture_burns, parsed_design)
    aggregate_feasible = all(p[frame].get("feasible") for frame in AGGREGATE_FRAMES)
    fixture_feasible = all(
        fixture_p[frame][fixture].get("feasible")
        for frame in PRECISION_FRAMES
        for fixture in SEALED_FIXTURES
    )

    result = {
        "schema": "t111-gate2-v2-stage2-preparation-v3",
        "mode": "synthetic" if a.synthetic else "sealed",
        "coordinates_total": len(coords),
        "burned": len(burns),
        "freed": len(mapped) - len(burns),
        "burns_unmapped_to_any_frame": unmapped,
        "burns_by_frame": frame_burns,
        "burns_by_precision_fixture": fixture_burns,
        "burn_rules": sorted({m["rule"] for m in burns}),
        "power": p,
        "per_fixture_power": fixture_p,
        "all_frames_feasible": aggregate_feasible and fixture_feasible,
    }

    # All fallible computation completes before the final output directory
    # exists. Publication stages the complete pair and renames it atomically.
    try:
        publish_outputs(out, mapped, result)
    except FileExistsError:
        print(f"stage2: refusing — output {out} already exists", file=sys.stderr)
        return 3
    print(json.dumps(result, indent=2, sort_keys=True))
    return 0 if result["all_frames_feasible"] else 1


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--ledger", required=True)
    ap.add_argument("--heads", required=True)
    ap.add_argument("--cardinalities", required=True)
    ap.add_argument("--frame-membership", dest="frame_membership", required=True)
    ap.add_argument("--design", required=True)
    ap.add_argument("--receipt")
    ap.add_argument("--synthetic", action="store_true")
    ap.add_argument("--out", required=True)
    try:
        return run(ap.parse_args())
    except Stage2Error as exc:
        print(f"stage2: refusing — {exc}", file=sys.stderr)
        return 2
    except Exception as exc:
        print(f"stage2: unexpected failure: {type(exc).__name__}", file=sys.stderr)
        return 4


if __name__ == "__main__":
    raise SystemExit(main())
