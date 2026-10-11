"""T47.2b sampling-frame preparation (protocol sections 5-6).

Builds the preregistered frames from committed, source-derived artifacts
only. No Phebs prediction, label, score or seed value is consumed here.

Frames are source recall (all call expressions, admitted method references,
full-method strings), a source declaration-interface census, and pending
candidate precision/abstention/unresolved frames. Imports alone are the sole
excluded evidence kind. Interface labels support the independent citation
ledger; they do not substitute for call-site attributed-edge metrics.

Sample sizes come from the approved parameters file (97 per frame stratum,
complete census for smaller strata). The seed-dependent draw is a separate
function invoked only after the sealed randomness commitment exists.
"""

from __future__ import annotations

import argparse
import gzip
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

FRAMES_PLAN_SCHEMA = "t472-frames-plan-v2"
RECALL_FRAME_SCHEMA = "t472-recall-frame-v2"
ATTRIBUTION_FRAME_SCHEMA = "t472-attribution-frame-v2"
CANDIDATE_FRAME_SCHEMA = "t472-candidate-frame-v3"

RECALL_ELIGIBLE_KINDS = frozenset({"constructor_call", "operation_invocation",
                                    "indirect_or_other_call", "operation_reference",
                                    "full_method_string"})
EXCLUDED_REASONS = {"import_reference": "an import alone is not an invocation; every call and admitted method reference remains eligible"}
QUALITY_FRAMES = {"caller_precision": "precision", "caller_recall": "recall",
                  "attributed_edge_precision": "precision", "attributed_edge_recall": "recall",
                  "unresolved_accuracy": "unresolved"}
MISSING_BINDINGS = [
    "sealed proof-bundle-v1 envelope identity and sha256 (precision, unresolved frames)",
    "phebs complete candidate ledger identity and sha256 (abstention frame)",
    "NIST beacon pulse and derived 64-hex seed for the sample draw",
    "frozen blind label document per frame (Ben, sole owner/reviewer)",
    "independent expected-resolution ledger sha256",
    "independent declaration-citation ledger sha256",
    "phebs eligible-unit outcome receipt for the processing-state ledger",
    "protocol digest after the section 10 sealing checklist completes",
]
CANDIDATE_PREDICATES = {
    "CALLS_OPERATION": "precision",
    "UNRESOLVED_CALLER": "unresolved",
}


class FramesError(ValueError):
    """A frame input or population violates the preregistration rules."""


def sha256_file(path: Path) -> str:
    return protocol.sha256_bytes(path.read_bytes())


def load_jsonl(path: Path) -> list[dict]:
    opener = gzip.open if path.suffix == ".gz" else open
    with opener(path, "rt", encoding="utf-8") as f:
        return [json.loads(line) for line in f if line.strip()]


def load_universe(universe_dir: Path) -> dict:
    summary = json.loads((universe_dir / "universe.json").read_text())
    if summary.get("schema") != "t472-universe-v2":
        raise FramesError("universe schema must be t472-universe-v2")
    sites, site_files = [], {}
    for name, expected in sorted(summary["sites"]["sha256"].items()):
        if Path(name).name != name:
            raise FramesError("universe member is not a flat file name")
        f = universe_dir / name
        if sha256_file(f) != expected:
            raise FramesError(f"{name}: universe member digest mismatch")
        rows = load_jsonl(f)
        sites.extend(rows)
        site_files[name] = {"file": name, "rows": len(rows), "sha256": expected}
    decl_path = universe_dir / "universe.declarations.jsonl"
    declarations = load_jsonl(decl_path)
    if sha256_file(decl_path) != summary["declarations"]["sha256"]:
        raise FramesError("declaration ledger digest mismatch")
    if len(sites) != summary["sites"]["total"] or len(declarations) != summary["declarations"]["generated_files"]:
        raise FramesError("universe populations do not reconcile")
    return {"sites": sites, "declarations": declarations, "summary": summary,
            "site_files": site_files, "declarations_sha256": sha256_file(decl_path),
            "summary_sha256": sha256_file(universe_dir / "universe.json")}


def recall_eligibility(row: Mapping) -> tuple[bool, str | None]:
    """Return (eligible, exclusion_reason) for one universe site row."""
    kind = row.get("kind")
    if kind in RECALL_ELIGIBLE_KINDS:
        return True, None
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
    """One exact source unit per constructor-backed service-client interface."""
    units = []
    for d in declarations:
        for interface in d["interfaces"]:
            start, end = interface["start_byte"], interface["end_byte"]
            if type(start) is not int or type(end) is not int or start < 0 or end <= start:
                raise FramesError(f"{d['generated_path']}: invalid interface span")
            units.append({
                "site_id": f"{d['repository']}@{d['commit']}:{d['generated_path']}:{start}-{end}",
                "stratum": f"{d['repository']}:{interface['code_role']}",
                "schema": ATTRIBUTION_FRAME_SCHEMA, "service": interface["service"],
                "generated_path": d["generated_path"], "import_path": d["import_path"],
                "mapped": d["mapped"], "vendored": d["vendored"],
                "abstention_reason": d.get("abstention_reason"),
                "operations": sum(o["service"] == interface["service"] for o in d["operations"]),
            })
    if len({u["site_id"] for u in units}) != len(units):
        raise FramesError("duplicate attribution interface coordinate")
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


def project_candidate_frames(envelope: Mapping, universe: Mapping,
                             sites_per_stratum: int = 97) -> dict[str, dict]:
    """Adapt production caller facts, preserving source identities and declaration lineage."""
    citations = {}
    for site in universe["sites"]:
        if not recall_eligibility(site)[0]:
            continue
        prefix = f"{site['repository']}@{site['commit']}:{site['path']}:"
        for start, end in site["citation_spans"]:
            raw = prefix + f"{start}-{end}"
            if raw in citations and citations[raw]["site_id"] != site["site_id"]:
                raise FramesError("ambiguous source citation coordinate")
            citations[raw] = site
    bundle = envelope["bundle"]
    assertions = []
    reasons = {}
    claims = {}
    evidence = {(e["repository"], e["run_id"], e["atom"]["id"]): e
                for e in bundle["evidence"]}
    declaration_predicates = {"DECLARES_FIELD", "DECLARES_MESSAGE", "DECLARES_SERVICE", "DECLARES_OPERATION"}
    for a in bundle["assertions"]:
        predicate = a["predicate"]
        if predicate in declaration_predicates:
            continue  # declaration facts are source-ledger inputs, not caller predictions
        if predicate not in CANDIDATE_PREDICATES:
            raise FramesError(f"unsupported caller predicate: {predicate}")
        obj = a["object"]
        if not isinstance(obj, str) or not re.fullmatch(r"/[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)+/[A-Za-z_]\w*", obj):
            raise FramesError("caller object must be /package.Service/Method")
        detail = json.loads(a["detail"])
        if detail.get("schema") != "go-caller-detail-v1" or detail.get("protocol") != "grpc":
            raise FramesError("candidate is not a grpc-caller fact")
        reason = detail.get("unresolved_reason", "")
        if not isinstance(reason, str) or (predicate == "UNRESOLVED_CALLER") != bool(reason):
            raise FramesError("caller resolution and reason disagree")
        if not isinstance(a.get("lineage", ""), str) or predicate == "CALLS_OPERATION" and not a.get("lineage"):
            raise FramesError("resolved caller has no declaration lineage")
        adapted = {**a, "object": obj[1:],
                   "predicate": "UNRESOLVED_GRPC_CALL" if predicate == "UNRESOLVED_CALLER" else predicate}
        assertions.append(adapted)
        for atom_id in a["supporting"]:
            e = evidence[(a["repo"], a["run_id"], atom_id)]
            atom = e["atom"]
            for occurrence in e["occurrences"]:
                raw = f"{a['repo']}@{occurrence['commit']}:{occurrence['path']}:{atom['start_byte']}-{atom['end_byte']}"
                source = citations.get(raw)
                if source is None:
                    raise FramesError(f"candidate citation is outside the exact source universe: {raw}")
                if a.get("code_role") != source["code_role"]:
                    raise FramesError("candidate code role disagrees with independent source role")
                claim = (predicate, a.get("lineage", ""), reason)
                previous = claims.setdefault(source["site_id"], {})
                # Validate raw duplicates before the frozen harness can discard them.
                if adapted["object"] in previous and previous[adapted["object"]] != claim:
                    raise FramesError("conflicting claims for one operation at a caller site")
                if previous and (next(iter(previous.values()))[0] != predicate or
                                 predicate == "CALLS_OPERATION" and adapted["object"] not in previous):
                    raise FramesError("conflicting resolution claims at one caller site")
                previous[adapted["object"]] = claim
                key = (raw, adapted["predicate"], adapted["object"], a.get("lineage", ""))
                reasons[key] = reason
    rows = pilot_harness.candidate_rows({"bundle": {**bundle, "assertions": assertions}})
    projected = {name: {} for name in CANDIDATE_PREDICATES.values()}
    for row in rows:
        source = citations[row["site_id"]]
        predicate = "UNRESOLVED_CALLER" if row["predicate"] == "UNRESOLVED_GRPC_CALL" else row["predicate"]
        canonical = {**row, "site_id": source["site_id"], "source_citation": row["site_id"],
                     "predicate": predicate, "stratum": source["stratum"],
                     "unresolved_reason": reasons[(row["site_id"], row["predicate"], row["object"], row["lineage"])]}
        key = source["site_id"]
        frame_id = CANDIDATE_PREDICATES[predicate]
        if key not in projected[frame_id]:
            projected[frame_id][key] = canonical
            if frame_id == "unresolved":
                canonical["alternatives"] = {}
        if frame_id == "unresolved":
            alternatives = projected[frame_id][key]["alternatives"]
            alternatives.setdefault(row["object"], {
                "operation": row["object"], "reason": canonical["unresolved_reason"],
                "lineage": row["lineage"], "source_citation": row["site_id"],
            })
    frames = {}
    for name, by_site in projected.items():
        members = list(by_site.values())
        if name == "unresolved":
            for member in members:
                alternatives = member["alternatives"]
                member["alternatives"] = [alternatives[op] for op in sorted(alternatives)]
                if len(alternatives) > 1:
                    member.update(object=None, lineage=None, unresolved_reason=None)
        strata = stratify_population(members)
        frames[name] = {"schema": CANDIDATE_FRAME_SCHEMA, "rows": members,
                        "site_ids": sorted(r["site_id"] for r in members), "population": len(members),
                        "strata": {s: {"site_ids": ids, **frame_strata_sizes({s: ids}, sites_per_stratum)[s]}
                                   for s, ids in strata.items()}}
    return frames


def load_frame_populations(plan: Mapping, frames_dir: Path) -> dict:
    populations = {}
    for frame in plan["frames"]:
        if "file" not in frame:
            continue
        if Path(frame["file"]).name != frame["file"]:
            raise FramesError("frame member must be a flat filename")
        path = frames_dir / frame["file"]
        if sha256_file(path) != frame["sha256"]:
            raise FramesError("frame population digest mismatch")
        populations[frame["frame_id"]] = load_jsonl(path)
    return populations


def _planned_strata(frame: Mapping, populations: Mapping) -> dict:
    if frame["frame_id"] not in populations:
        raise FramesError("frame population is missing")
    strata = stratify_population(populations[frame["frame_id"]])
    if set(strata) != set(frame["strata"]) or sum(map(len, strata.values())) != frame["population"]:
        raise FramesError("frame population does not reconcile to the plan")
    all_ids = [site_id for ids in strata.values() for site_id in ids]
    if len(set(all_ids)) != len(all_ids):
        raise FramesError("frame population has duplicate coordinates")
    for stratum, ids in strata.items():
        if len(ids) != frame["strata"][stratum]["population"]:
            raise FramesError("stratum population disagrees with the plan")
    return strata


def draw_samples(plan: Mapping, seed_hex: str, populations: Mapping) -> dict:
    """Draw from digest-bound populations only after the real sealed seed exists."""
    if not re.fullmatch(r"[0-9a-f]{64}", seed_hex or ""):
        raise FramesError("seed must be 64 lowercase hex characters")
    drawn = {}
    for frame in plan["frames"]:
        if "strata" not in frame:
            continue
        strata = _planned_strata(frame, populations)
        sizes = {name: p["sample_size"] for name, p in frame["strata"].items()}
        drawn[frame["frame_id"]] = pilot_harness.stratified_sample(strata, sizes, seed_hex)
    return drawn


def quality_samples(plan: Mapping, drawn: Mapping, populations: Mapping) -> dict:
    """Reconcile the sealed draw with every approved quality family's frame."""
    if plan.get("quality_frames") != QUALITY_FRAMES:
        raise FramesError("quality-family frame mapping is incomplete")
    frame_by_id = {f["frame_id"]: f for f in plan["frames"]}
    required = {}
    for family, frame_id in QUALITY_FRAMES.items():
        frame = frame_by_id[frame_id]
        if frame.get("pending") or "strata" not in frame or frame_id not in drawn:
            raise FramesError("candidate frame or planned draw is still missing")
        population = _planned_strata(frame, populations)
        draw = drawn[frame_id]
        if set(draw) != set(frame["strata"]):
            raise FramesError("draw omits or adds a planned stratum")
        for stratum, ids in draw.items():
            if len(ids) != frame["strata"][stratum]["sample_size"] or len(set(ids)) != len(ids) \
                    or not set(ids) <= set(population[stratum]):
                raise FramesError("draw does not complete the planned sample/census")
        required[family] = draw
    return required


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
        (out_dir / name).write_bytes(gzip.compress(body, mtime=0) if name.endswith(".gz") else body)
        return {"file": name, "rows": len(rows), "sha256": sha256_file(out_dir / name)}

    recall_strata_pop = {
        name: frame_strata_sizes({name: ids}, sites_per_stratum)[name]
        for name, ids in recall_strata.items()
    }
    attribution_strata_pop = {
        name: frame_strata_sizes({name: ids}, sites_per_stratum)[name]
        for name, ids in attribution_strata.items()
    }

    recall_ref = dump_jsonl("frames.recall.jsonl.gz", recall_population)
    attribution_ref = dump_jsonl("frames.attribution.jsonl", attribution_units)
    (out_dir / "frames.excluded.json").write_text(json.dumps(
        {"schema": "t472-excluded-ledger-v1", **excluded}, indent=1, sort_keys=True) + "\n")

    plan = {
        "schema": FRAMES_PLAN_SCHEMA,
        "frames": [
            {
                "frame_id": "recall", "claim_family": "caller_sites",
                "frame_schema": RECALL_FRAME_SCHEMA, "label_fields": ["invocation", "operation"],
                "population_rule": "every Go call expression, admitted method reference and full-method string; imports alone stay in the excluded ledger",
                "strata": recall_strata_pop, "population": len(recall_population),
                **{k: recall_ref[k] for k in ("file", "rows", "sha256")},
            },
            {
                "frame_id": "attribution", "claim_family": "declaration_attribution",
                "frame_schema": ATTRIBUTION_FRAME_SCHEMA, "label_fields": ["registration", "service"],
                "population_rule": ("one unit per exact constructor-backed service-client interface; repository x code_role strata; this source census supports the independent declaration-citation ledger"),
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
                "population_rule": "projected from the sealed envelope, predicate UNRESOLVED_CALLER via the caller-specific adapter",
                "pending": True,
            },
        ],
        "sampling": {"sites_per_frame_stratum": sites_per_stratum,
                     "smaller_strata": parameters["sampling"]["smaller_strata"],
                     "draw": "draw_samples(plan, sealed seed, digest-bound populations) via frozen harness.stratified_sample; "
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
        "quality_frames": QUALITY_FRAMES,
        "missing_bindings": MISSING_BINDINGS,
        "review_required": [
            "confirm source-only recall labeling over every Go call expression and inventoried method reference, with imports alone excluded",
            "confirm exact individual service-client interface coordinates for the declaration-citation source census",
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
