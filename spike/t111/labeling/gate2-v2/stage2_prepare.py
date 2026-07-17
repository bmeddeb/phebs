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
    if not receipt.get("response_sha256") or not receipt.get("heads"):
        raise Stage2Error("receipt lacks response digest or sealed heads")
    return receipt


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


def run(a) -> int:
    if not a.synthetic:
        if not a.receipt:
            print("stage2: refusing — no Stage-1 receipt and not --synthetic", file=sys.stderr)
            return 2
        try:
            verify_receipt(a.receipt)
        except Stage2Error as exc:
            print(f"stage2: refusing — {exc}", file=sys.stderr)
            return 2

    out = Path(a.out)
    try:
        out.mkdir(mode=0o700)
    except FileExistsError:
        print(f"stage2: refusing — output {out} already exists", file=sys.stderr)
        return 3

    coords = ledger_coordinates(load_json(a.ledger))
    mapped = carry_forward(coords, load_json(a.heads))
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
    (out / "census-v2-seed.jsonl").write_text(
        "".join(json.dumps(m, sort_keys=True) + "\n" for m in mapped), encoding="utf-8")
    (out / "stage2-preparation.json").write_text(
        json.dumps(result, indent=2, sort_keys=True) + "\n", encoding="utf-8")
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
    return run(ap.parse_args())


if __name__ == "__main__":
    raise SystemExit(main())
