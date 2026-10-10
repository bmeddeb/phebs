"""T47.2b sampling-frame preparation (protocol sections 5-6).

Builds the preregistered frames from committed, source-derived artifacts
only. No Phebs prediction, label, score or seed value is consumed here.

Frames:
  recall       source-based; population from the universe enumeration
               (kinds constructor_call, full_method_string, and
               client_aware operation_invocation). Bare method-name tokens
               in files that reference no committed generated client, and
               client imports alone, are accounted in an excluded ledger
               with an explicit reason instead of being sampled or dropped.
  attribution  one unit per committed generated client (mapped or
               abstained); the unit coordinate is the generated file's
               constructor-backed client-interface span. The frozen t111
               schema labels these registration yes/no + canonical service,
               so an abstained client is truthfully registration=no.
  precision, abstention, unresolved
               candidate-derived; projected from one sealed proof-bundle-v1
               envelope through the frozen pilot harness at sealing time.
               Their identities are recorded as missing bindings here.

Sample sizes come from the approved parameters file (97 per frame stratum,
complete census for smaller strata). The seed-dependent draw is a separate
function invoked only after the sealed randomness commitment exists.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
from collections.abc import Mapping, Sequence
from pathlib import Path

_SPIKE_DIR = Path(__file__).resolve().parent
for _extra in ("../t111", "../../pilot/validation"):
    p = (_SPIKE_DIR / _extra).resolve()
    if str(p) not in sys.path:
        sys.path.insert(0, str(p))

import label_protocol as protocol  # noqa: E402
import harness as pilot_harness  # noqa: E402

FRAMES_PLAN_SCHEMA = "t472-frames-plan-v1"
RECALL_FRAME_SCHEMA = "t472-recall-frame-v1"
ATTRIBUTION_FRAME_SCHEMA = "t472-attribution-frame-v1"
CANDIDATE_FRAME_SCHEMA = "t472-candidate-frame-v1"

RECALL_ELIGIBLE_KINDS = frozenset({"constructor_call", "full_method_string"})
EXCLUDED_REASONS = {
    "import_reference": ("import of a generated client path alone is not call "
                         "evidence; dynamic/aliased construction cannot be tied "
                         "to the frozen client inventory from the import"),
    "operation_invocation_not_client_aware": ("bare method-name token in a file "
                                              "referencing no committed generated "
                                              "client; cannot be distinguished from "
                                              "same-name methods on unrelated types"),
}
SITE_ID_RE = re.compile(
    r"\A(?P<repo>[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)@(?P<commit>[0-9a-f]{40}):"
    r"(?P<path>[^\s:]+):(?P<start>[0-9]+)-(?P<end>[0-9]+)\Z"
)
CANDIDATE_PREDICATES = {
    "CALLS_OPERATION": "precision",
    "UNRESOLVED_GRPC_CALL": "unresolved",
}


class FramesError(ValueError):
    """A frame input or population violates the preregistration rules."""


def sha256_file(path: Path) -> str:
    return protocol.sha256_bytes(path.read_bytes())


def load_jsonl(path: Path) -> list[dict]:
    with path.open() as f:
        return [json.loads(line) for line in f if line.strip()]


def load_universe(universe_dir: Path) -> dict:
    sites: list[dict] = []
    site_files = {}
    for f in sorted(universe_dir.glob("universe.sites.*.jsonl")):
        rows = load_jsonl(f)
        sites.extend(rows)
        site_files[f.name] = {"path": str(f), "rows": len(rows), "sha256": sha256_file(f)}
    declarations = load_jsonl(universe_dir / "universe.declarations.jsonl")
    summary = json.loads((universe_dir / "universe.json").read_text())
    return {"sites": sites, "declarations": declarations, "summary": summary,
            "site_files": site_files,
            "declarations_sha256": sha256_file(universe_dir / "universe.declarations.jsonl"),
            "summary_sha256": sha256_file(universe_dir / "universe.json")}


def recall_eligibility(row: Mapping) -> tuple[bool, str | None]:
    """Return (eligible, exclusion_reason) for one universe site row."""
    kind = row.get("kind")
    if kind in RECALL_ELIGIBLE_KINDS:
        return True, None
    if kind == "operation_invocation":
        if row.get("client_aware"):
            return True, None
        return False, "operation_invocation_not_client_aware"
    if kind == "import_reference":
        return False, "import_reference"
    raise FramesError(f"unknown universe site kind: {kind!r}")


def build_recall_frame(sites: Sequence[dict]) -> tuple[list[dict], dict]:
    """Split universe sites into the recall population and the excluded ledger."""
    population: list[dict] = []
    excluded: dict[str, dict] = {}
    for row in sites:
        eligible, reason = recall_eligibility(row)
        if eligible:
            population.append({
                "site_id": row["site_id"], "stratum": row["stratum"],
                "kind": row["kind"], "symbol": row["symbol"],
                "repository": row["repository"], "path": row["path"],
                "start_byte": row["start_byte"], "end_byte": row["end_byte"],
            })
        else:
            bucket = excluded.setdefault(reason, {"reason": EXCLUDED_REASONS[reason],
                                                  "by_stratum": {}})
            bucket["by_stratum"][row["stratum"]] = bucket["by_stratum"].get(row["stratum"], 0) + 1
    for reason, bucket in excluded.items():
        bucket["total"] = sum(bucket["by_stratum"].values())
    ids = [r["site_id"] for r in population]
    if len(set(ids)) != len(ids):
        raise FramesError("recall population contains duplicate site IDs")
    return population, {"excluded_kinds": excluded, "excluded_total": sum(b["total"] for b in excluded.values())}


def build_attribution_frame(declarations: Sequence[dict]) -> list[dict]:
    """One attribution unit per committed generated client."""
    units: list[dict] = []
    for d in declarations:
        start, end = d["interface_start_byte"], d["interface_end_byte"]
        if not isinstance(start, int) or not isinstance(end, int) or start < 0 or end <= start:
            raise FramesError(f"{d['generated_path']}: interface span is inconsistent")
        site_id = f"{d['repository']}@{d['commit']}:{d['generated_path']}:{start}-{end}"
        units.append({
            "site_id": site_id, "stratum": d["repository"], "schema": ATTRIBUTION_FRAME_SCHEMA,
            "generated_path": d["generated_path"], "import_path": d["import_path"],
            "mapped": d["mapped"], "vendored": d["vendored"],
            "abstention_reason": d.get("abstention_reason"),
            "operations": len(d["operations"]),
        })
    ids = [u["site_id"] for u in units]
    if len(set(ids)) != len(ids):
        raise FramesError("attribution frame contains duplicate site IDs")
    return units


def stratify_population(population: Sequence[Mapping], key: str = "stratum") -> dict[str, list[str]]:
    strata: dict[str, list[str]] = {}
    for row in population:
        strata.setdefault(row[key], []).append(row["site_id"])
    return {name: sorted(ids) for name, ids in sorted(strata.items())}


def frame_strata_sizes(strata: Mapping[str, Sequence[str]], sites_per_stratum: int) -> dict[str, dict]:
    sizes: dict[str, dict] = {}
    for name in sorted(strata):
        n = len(strata[name])
        sizes[name] = {"population": n, "sample_size": min(sites_per_stratum, n),
                       "census": n <= sites_per_stratum}
    return sizes


def project_candidate_frames(envelope: Mapping, corpus_commits: set[str]) -> dict[str, dict]:
    """Project one sealed proof-bundle-v1 envelope into candidate frames.

    Uses the frozen pilot harness candidate_rows. Every projected site ID
    must parse and name a corpus derived commit, and candidate-derived
    frames must stay blind: this function reads predictions, never labels.
    """
    rows = pilot_harness.candidate_rows(envelope)
    frames: dict[str, dict] = {}
    for row in rows:
        match = SITE_ID_RE.match(row["site_id"])
        if not match:
            raise FramesError(f"candidate site_id is not a canonical coordinate: {row['site_id']}")
        if match.group("commit") not in corpus_commits:
            raise FramesError(f"candidate site_id names a commit outside the corpus: {row['site_id']}")
        if int(match.group("start")) >= int(match.group("end")):
            raise FramesError(f"candidate site_id span is inconsistent: {row['site_id']}")
    for predicate, frame_name in CANDIDATE_PREDICATES.items():
        ids = sorted({r["site_id"] for r in rows if r["predicate"] == predicate})
        frames[frame_name] = {"schema": CANDIDATE_FRAME_SCHEMA, "predicate": predicate,
                              "site_ids": ids, "population": len(ids)}
    return frames


def draw_samples(plan: Mapping, seed_hex: str) -> dict[str, list[str]]:
    """Seed-dependent per-stratum draw through the frozen harness.

    Invoked only after the sealed randomness commitment exists; identical
    inputs always select identical samples so a third party can re-derive.
    """
    if not re.fullmatch(r"[0-9a-f]{64}", seed_hex or ""):
        raise FramesError("seed must be 64 lowercase hex characters")
    drawn: dict[str, list[str]] = {}
    for frame in plan["frames"]:
        if "strata" not in frame:
            continue  # candidate-derived frames draw only after their envelope seals
        strata = {name: pop["site_ids"] for name, pop in frame["strata"].items()}
        sizes = {name: pop["sample_size"] for name, pop in frame["strata"].items()}
        drawn[frame["frame_id"]] = pilot_harness.stratified_sample(strata, sizes, seed_hex)
    return drawn


def write_frames(repo_root: Path, universe_dir: Path, out_dir: Path,
                 parameters_path: Path, census_path: Path, lock_path: Path) -> dict:
    parameters = json.loads(parameters_path.read_text())
    if parameters.get("schema") != "t472-quality-parameters-v1":
        raise FramesError("parameters file is not t472-quality-parameters-v1")
    sites_per_stratum = int(parameters["sampling"]["sites_per_frame_stratum"])

    universe = load_universe(universe_dir)
    recall_population, excluded = build_recall_frame(universe["sites"])
    recall_strata = stratify_population(recall_population)
    attribution_units = build_attribution_frame(universe["declarations"])
    attribution_strata = stratify_population(attribution_units, key="stratum")

    corpus = json.loads(lock_path.read_text())
    corpus_commits = {r["derived"]["derived_commit"] for r in corpus["repos"]
                      if r["derived"].get("corpus_admitted")}

    out_dir.mkdir(parents=True, exist_ok=True)

    def dump_jsonl(name: str, rows: Sequence[Mapping]) -> dict:
        body = b"".join(json.dumps(dict(r), ensure_ascii=False, allow_nan=False,
                                   sort_keys=True, separators=(",", ":")).encode("utf-8") + b"\n"
                        for r in rows)
        (out_dir / name).write_bytes(body)
        return {"file": name, "rows": len(rows), "sha256": protocol.sha256_bytes(body)}

    recall_strata_pop = {
        name: {"site_ids": ids, **frame_strata_sizes({name: ids}, sites_per_stratum)[name]}
        for name, ids in recall_strata.items()
    }
    attribution_strata_pop = {
        name: {"site_ids": ids, **frame_strata_sizes({name: ids}, sites_per_stratum)[name]}
        for name, ids in attribution_strata.items()
    }

    recall_ref = dump_jsonl("frames.recall.jsonl", recall_population)
    attribution_ref = dump_jsonl("frames.attribution.jsonl", attribution_units)
    (out_dir / "frames.excluded.json").write_text(json.dumps(
        {"schema": "t472-excluded-ledger-v1", **excluded}, indent=1, sort_keys=True) + "\n")

    plan = {
        "schema": FRAMES_PLAN_SCHEMA,
        "frames": [
            {
                "frame_id": "recall", "claim_family": "caller_sites",
                "frame_schema": RECALL_FRAME_SCHEMA, "label_fields": ["invocation", "operation"],
                "population_rule": ("universe kinds constructor_call and full_method_string, plus "
                                    "operation_invocation rows whose file references a committed "
                                    "generated client (client_aware); excluded kinds are accounted "
                                    "in frames.excluded.json, never sampled and never dropped silently"),
                "strata": recall_strata_pop, "population": len(recall_population),
                **{k: recall_ref[k] for k in ("file", "rows", "sha256")},
            },
            {
                "frame_id": "attribution", "claim_family": "declaration_attribution",
                "frame_schema": ATTRIBUTION_FRAME_SCHEMA, "label_fields": ["registration", "service"],
                "population_rule": ("one unit per committed generated client at its "
                                    "constructor-backed client-interface span; mapped and abstained "
                                    "clients both populate the frame"),
                "strata": attribution_strata_pop, "population": len(attribution_units),
                **{k: attribution_ref[k] for k in ("file", "rows", "sha256")},
            },
            {
                "frame_id": "precision", "claim_family": "caller_sites",
                "frame_schema": CANDIDATE_FRAME_SCHEMA, "label_fields": ["invocation", "operation"],
                "population_rule": "projected from the sealed proof-bundle-v1 envelope via pilot harness candidate_rows, predicate CALLS_OPERATION",
                "pending": True,
            },
            {
                "frame_id": "abstention", "claim_family": "caller_sites",
                "frame_schema": CANDIDATE_FRAME_SCHEMA, "label_fields": ["invocation", "operation"],
                "population_rule": ("units phebs considered but did not report as call sites "
                                    "(its complete candidate ledger, reported plus withheld); "
                                    "not derivable from proof-bundle assertions alone, so its "
                                    "population binds with the sealed run receipt"),
                "pending": True,
            },
            {
                "frame_id": "unresolved", "claim_family": "caller_sites",
                "frame_schema": CANDIDATE_FRAME_SCHEMA, "label_fields": ["invocation", "operation"],
                "population_rule": "projected from the sealed envelope, predicate UNRESOLVED_GRPC_CALL",
                "pending": True,
            },
        ],
        "sampling": {"sites_per_frame_stratum": sites_per_stratum,
                     "smaller_strata": parameters["sampling"]["smaller_strata"],
                     "draw": "draw_samples(plan, sealed seed) via frozen harness.stratified_sample; "
                             "not executed before the randomness commitment"},
        "provenance": {
            "universe_sites": universe["site_files"],
            "universe_declarations_sha256": universe["declarations_sha256"],
            "universe_summary_sha256": universe["summary_sha256"],
            "parameters_sha256": sha256_file(parameters_path),
            "source_census_sha256": sha256_file(census_path),
            "corpus_lock_sha256": sha256_file(lock_path),
            "enumerate_universe_sha256": sha256_file(_SPIKE_DIR / "enumerate_universe.py"),
            "frames_py_sha256": sha256_file(Path(__file__)),
        },
        "corpus_derived_commits": sorted(corpus_commits),
        "candidate_blind_source_labels": True,
        "missing_bindings": [
            "sealed proof-bundle-v1 envelope identity and sha256 (precision, unresolved frames)",
            "phebs complete candidate ledger identity and sha256 (abstention frame)",
            "NIST beacon pulse and derived 64-hex seed for the sample draw",
            "frozen blind label document per frame (Ben, sole owner/reviewer)",
            "phebs eligible-unit outcome receipt for the processing-state ledger",
            "protocol digest after the section 10 sealing checklist completes",
        ],
        "review_required": [
            ("confirm the recall exclusion rules: bare method-name tokens in files referencing no "
             "committed generated client, and client imports alone, stay out of recall sampling "
             "(aliases, wrappers, dynamic paths and same-name methods cannot be resolved to the "
             "frozen client inventory from source alone)"),
            "confirm attribution units use the whole constructor-backed interface span as the coordinate",
        ],
        "labels_committed": False, "seed_committed": False, "quality_scored": False,
    }
    (out_dir / "frames.plan.json").write_text(json.dumps(plan, indent=1, sort_keys=True) + "\n")
    return plan


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--repo-root", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--universe-dir", type=Path, default=None)
    parser.add_argument("--out-dir", type=Path, required=True)
    args = parser.parse_args()
    spike = args.repo_root / "spike" / "t472"
    plan = write_frames(
        repo_root=args.repo_root,
        universe_dir=args.universe_dir or spike / "universe",
        out_dir=args.out_dir,
        parameters_path=spike / "validation.parameters.json",
        census_path=spike / "source.census.json",
        lock_path=spike / "corpus.lock.json",
    )
    for frame in plan["frames"]:
        if frame.get("pending"):
            print(f"{frame['frame_id']}: PENDING (candidate-derived; sealed at sealing)")
        else:
            print(f"{frame['frame_id']}: population={frame['population']} "
                  f"strata={len(frame['strata'])} file={frame['file']} sha256={frame['sha256']}")
    print(f"excluded ledger: {json.loads((args.out_dir / 'frames.excluded.json').read_text())['excluded_total']}")


if __name__ == "__main__":
    main()
