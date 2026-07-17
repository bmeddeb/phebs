#!/usr/bin/env python3
"""GATE2-V2 Stage 2 — carry-forward and exact-power preparation glue.

Wires the sealed Stage-0 tools together for the run that happens after the
Stage-1 heads seal: historical burn-ledger coordinates → carry-forward
mapper (burn on doubt) → census-v2 seed burns → exact power against actual
frame cardinalities net of burns. Fails closed at every step.

This module is pure glue: it embeds no thresholds, no sampling rules, and
no population knowledge. It is tested exclusively against synthetic
fixtures until Stage 1 has sealed; running it against real inputs before
then would violate the ceremony's disclosure rules and is refused unless
the Stage-1 admission receipt exists.

Inputs (paths supplied by the caller):
  --ledger        burn-ledger JSON (cohorts[].coordinates[])
  --heads         JSON {fixture: {"old_commit":…, "new_commit":…, "repo_dir":…}}
  --cardinalities JSON {frame: {"population":N, "census":C}} from the sealed
                  frame-enumeration step (run separately, post-seal)
  --receipt       Stage-1 receipt (must be status ADMITTED) — omitted only
                  under --synthetic
  --out           output directory (created fresh; refuses to overwrite)
"""

from __future__ import annotations

import argparse
import json
import sys
from fractions import Fraction
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE / "stage0"))
from carry_forward import classify  # noqa: E402


class Stage2Error(Exception):
    pass


def load_json(path: str):
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


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
            # A ledger system absent from the sealed heads cannot be mapped;
            # burn on doubt — its census entry survives unmapped.
            results.append({**c, "decision": "burn", "rule": "system-not-in-snapshot"})
            continue
        d = classify(head["repo_dir"], head["old_commit"], head["new_commit"],
                     c["path"], c["start_line"], c["end_line"])
        results.append({**c, "decision": d.decision, "rule": d.rule})
    return results


def power(cardinalities: dict, burns_by_frame: dict[str, int],
          design: dict) -> dict:
    sys.path.insert(0, str(HERE / "stage0"))
    from power_advisory import minimal_n
    out = {}
    for frame, card in cardinalities.items():
        pop = card["population"] - burns_by_frame.get(frame, 0)
        census = min(card["census"], pop)
        if pop <= 0:
            out[frame] = {"feasible": False, "reason": "population exhausted by burns"}
            continue
        n = minimal_n(pop, census, Fraction(design[frame]["p"]),
                      Fraction(design[frame]["threshold"]))
        out[frame] = {"population_net": pop, "census": census,
                      "minimal_sample_size": n, "feasible": n is not None}
    return out


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--ledger", required=True)
    ap.add_argument("--heads", required=True)
    ap.add_argument("--cardinalities", required=True)
    ap.add_argument("--design", required=True)
    ap.add_argument("--receipt")
    ap.add_argument("--synthetic", action="store_true",
                    help="synthetic test mode: no Stage-1 receipt required")
    ap.add_argument("--out", required=True)
    a = ap.parse_args()

    if not a.synthetic:
        if not a.receipt:
            print("stage2: refusing — no Stage-1 receipt and not --synthetic", file=sys.stderr)
            return 2
        receipt = load_json(a.receipt)
        if receipt.get("status") != "ADMITTED":
            print("stage2: refusing — Stage-1 receipt is not ADMITTED", file=sys.stderr)
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
    frees = [m for m in mapped if m["decision"] == "free"]

    cards = load_json(a.cardinalities)
    design = load_json(a.design)
    burns_by_frame = {f: len(burns) for f in cards}  # conservative: all burns count against every frame
    p = power(cards, burns_by_frame, design)

    result = {
        "schema": "t111-gate2-v2-stage2-preparation-v1",
        "mode": "synthetic" if a.synthetic else "sealed",
        "coordinates_total": len(coords),
        "burned": len(burns), "freed": len(frees),
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


if __name__ == "__main__":
    raise SystemExit(main())
