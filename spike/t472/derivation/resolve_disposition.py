#!/usr/bin/env python3
"""Resolve the T47.2b corpus disposition in corpus.lock.json under option A.

Ben routed the vitess/istio §2 bound miss on 2026-10-09: drop both and admit
the three committed repositories. This is a recorded selection change, never a
silent adjustment — every refusal fact (retained .tmp stream, raw root index,
authored-unplaced snapshots, missed bounds) is preserved, the frozen §2 bound
is unchanged, and the two clones stay at their pins. The script only mutates
the disposition/status/admitted fields; it rewrites the lock in place with
ordered keys so the diff is exactly the decision.

Idempotent: re-running sets the same resolved values.
"""

import collections
import json
import os

HERE = os.path.dirname(os.path.abspath(__file__))
LOCK = os.path.normpath(os.path.join(HERE, "..", "corpus.lock.json"))

RESOLVED_UTC = "2026-10-09"
ADMITTED = ["etcd-io/etcd", "containerd/containerd", "grpc/grpc-go"]
DROPPED = ["vitessio/vitess", "istio/istio"]
DECISION = (
    "drop vitessio/vitess and istio/istio; the admitted T47.2b corpus is the "
    "three committed repositories"
)
POST_DECISION = (
    "the §6 strata and §3 denominators are re-recorded against the "
    "three-repository admitted corpus before sealing; the dropped "
    "repositories' refusal evidence (retained unrenamed .tmp merged streams, "
    "raw root indexes, authored-unplaced gate-passed snapshots) is preserved "
    "and their clones remain at their pins with clean working trees; the "
    "frozen §2 bound is unchanged"
)
DROPPED_DISPOSITION = (
    "dropped from the admitted corpus under option A (Ben, " + RESOLVED_UTC +
    "); refusal evidence retained; see derivation.disposition"
)


def main() -> None:
    with open(LOCK, encoding="utf-8") as fh:
        lock = json.load(fh, object_pairs_hook=collections.OrderedDict)

    der = lock["derivation"]
    der["status"] = (
        "resolved: the admitted T47.2b corpus is the three committed "
        "repositories (etcd-io/etcd, containerd/containerd, grpc/grpc-go); "
        "vitessio/vitess and istio/istio were dropped under option A after "
        "refusing the frozen §2 bounds at derivation"
    )

    disp = der["disposition"]
    disp["status"] = "resolved — option A (Ben, " + RESOLVED_UTC + ")"
    disp["resolved_utc"] = RESOLVED_UTC
    disp["resolved_by"] = "Ben (option A routing)"
    disp["chosen"] = "A"
    disp["decision"] = DECISION
    disp["admitted_corpus"] = list(ADMITTED)
    disp["dropped"] = list(DROPPED)
    disp["post_decision"] = POST_DECISION
    for opt in disp.get("options", []):
        opt["chosen"] = opt.get("id") == "A"

    admitted_set = set(ADMITTED)
    for repo in lock["repos"]:
        der_repo = repo["derived"]
        admitted = repo["name"] in admitted_set
        # corpus_admitted sits inside derived, keeping repo top-level keys stable
        rebuilt = collections.OrderedDict()
        for key, value in der_repo.items():
            rebuilt[key] = value
            if key == "status":
                rebuilt["corpus_admitted"] = admitted
        if "corpus_admitted" not in rebuilt:
            rebuilt["corpus_admitted"] = admitted
        if not admitted:
            rebuilt["disposition"] = DROPPED_DISPOSITION
        repo["derived"] = rebuilt

    with open(LOCK, "w", encoding="utf-8") as fh:
        json.dump(lock, fh, indent=2, ensure_ascii=False)
        fh.write("\n")

    print("resolved disposition -> option A; admitted:", ", ".join(ADMITTED))
    print("dropped:", ", ".join(DROPPED))
    print("wrote", LOCK)


if __name__ == "__main__":
    main()
