#!/usr/bin/env python3
"""GATE2-V2 Stage 2 — carry-forward and exact-power preparation glue.

Wires the sealed Stage-0 tools together for the run after Stage-1 heads
seal: burn-ledger coordinates → carry-forward mapper (burn on doubt) →
census-v2 seed burns → exact power against frame-specific cardinalities
net of frame-specific burns. Fails closed at every step.

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
  --design           JSON {frame: {"p": "num/den", "threshold": "num/den"}}
  --receipt          Stage-1 receipt (schema/cutoff/query-digest bound);
                     required unless --synthetic
  --out              fresh output directory (refuses to overwrite)
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from fractions import Fraction
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE / "stage0"))
sys.path.insert(0, str(HERE))
from carry_forward import classify  # noqa: E402

RECEIPT_SCHEMA = "t111-gate2-v2-stage1-receipt-v1"


class Stage2Error(Exception):
    pass


def load_json(path: str):
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


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
    if set(heads) != set(receipt_heads):
        raise Stage2Error("heads fixtures do not exactly match the sealed Stage-1 receipt")
    for fixture, sealed in receipt_heads.items():
        if not isinstance(sealed, dict) or not sealed.get("head_oid"):
            raise Stage2Error(f"receipt lacks a sealed head oid for {fixture!r}")
        submitted = heads[fixture]
        if not isinstance(submitted, dict) or submitted.get("new_commit") != sealed["head_oid"]:
            raise Stage2Error(f"heads {fixture!r} new_commit does not match the sealed Stage-1 head")


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


def burns_per_frame(burns: list[dict], membership: dict,
                    frames: list[str]) -> tuple[dict[str, int], int]:
    """Frame-specific burn counts. A burned coordinate in no frame's
    membership burns against every frame (burn on doubt)."""
    member_keys = {f: set(membership.get(f, [])) for f in frames}
    counts = {f: 0 for f in frames}
    unmapped = 0
    for b in burns:
        k = coordinate_key(b)
        hit = [f for f in frames if k in member_keys[f]]
        if hit:
            for f in hit:
                counts[f] += 1
        else:
            unmapped += 1
            for f in frames:
                counts[f] += 1
    return counts, unmapped


def power(cardinalities: dict, frame_burns: dict[str, int], design: dict) -> dict:
    from power_advisory import minimal_n
    out = {}
    for frame, card in cardinalities.items():
        pop = card["population"] - frame_burns.get(frame, 0)
        census = min(card["census"], pop)
        if pop <= 0:
            out[frame] = {"feasible": False, "reason": "population exhausted by burns"}
            continue
        n = minimal_n(pop, census, Fraction(design[frame]["p"]),
                      Fraction(design[frame]["threshold"]))
        out[frame] = {"population_net": pop, "census": census,
                      "burns_applied": frame_burns.get(frame, 0),
                      "minimal_sample_size": n, "feasible": n is not None}
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
    if not a.synthetic:
        if not a.receipt:
            raise Stage2Error("no Stage-1 receipt and not --synthetic")
        receipt = verify_receipt(a.receipt)

    coords = ledger_coordinates(load_json(a.ledger))
    heads = load_json(a.heads)
    if receipt is not None:
        verify_heads(heads, receipt)
    mapped = carry_forward(coords, heads)
    burns = [m for m in mapped if m["decision"] == "burn"]

    cards = load_json(a.cardinalities)
    design = load_json(a.design)
    membership = load_json(a.frame_membership)
    frames = sorted(cards)
    frame_burns, unmapped = burns_per_frame(burns, membership, frames)
    p = power(cards, frame_burns, design)

    result = {
        "schema": "t111-gate2-v2-stage2-preparation-v2",
        "mode": "synthetic" if a.synthetic else "sealed",
        "coordinates_total": len(coords),
        "burned": len(burns),
        "freed": len(mapped) - len(burns),
        "burns_unmapped_to_any_frame": unmapped,
        "burns_by_frame": frame_burns,
        "burn_rules": sorted({m["rule"] for m in burns}),
        "power": p,
        "all_frames_feasible": all(v.get("feasible") for v in p.values()),
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
