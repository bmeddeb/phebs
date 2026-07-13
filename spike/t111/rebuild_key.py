#!/usr/bin/env python3
"""Refresh labeling/key.jsonl from current facts WITHOUT resampling sites.

The site list (sites.dev.jsonl + sites.holdout.jsonl) is frozen once labeled;
after extractor changes only the verdicts are recomputed, so labels stay valid
and the holdout stays unspent.
"""
import json
import os
from collections import defaultdict

BASE = os.path.dirname(os.path.abspath(__file__))
LAB = os.path.join(BASE, "labeling")
OUT = os.path.join(BASE, "out")
SYSTEMS = ["online-boutique", "dapr", "temporal", "loki"]


def relpath_of(fact_path, system):
    marker = f"corpus/{system}/"
    i = fact_path.find(marker)
    return fact_path[i + len(marker):] if i >= 0 else fact_path


def main():
    idx = defaultdict(list)  # (system, relpath, method) -> facts
    for system in SYSTEMS:
        with open(os.path.join(OUT, f"{system}.facts.jsonl")) as f:
            for line in f:
                fact = json.loads(line)
                if fact["predicate"] != "CALLS_OPERATION":
                    continue
                m = fact["object"].rsplit("/", 1)[-1]
                idx[(system, relpath_of(fact["path"], system), m)].append(fact)

    key = []
    for split in ("dev", "holdout"):
        with open(os.path.join(LAB, f"sites.{split}.jsonl")) as f:
            for line in f:
                s = json.loads(line)
                hit = None
                for fact in idx.get((s["system"], s["path"], s["method"]), []):
                    if fact["start_line"] <= s["line"] <= fact["end_line"]:
                        hit = fact
                        break
                k = {"site_id": s["site_id"], "extracted": bool(hit)}
                if hit:
                    k.update(object=hit["object"], role=hit["code_role"], tier=hit["tier"])
                key.append(k)

    with open(os.path.join(LAB, "key.jsonl"), "w") as f:
        for k in key:
            f.write(json.dumps(k) + "\n")
    print(f"rebuilt key: {len(key)} sites, {sum(1 for k in key if k['extracted'])} extracted")


if __name__ == "__main__":
    main()
