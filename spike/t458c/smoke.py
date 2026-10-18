#!/usr/bin/env python3
"""One reviewed neutral-whole demo integration; no rehearsal or accuracy claim."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import socket
import subprocess
import sys
import time

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
sys.path.insert(0, str(REPO / "spike/t503"))
import requalify as shared
import requalification_checks as checks
import score

OWNED = ("spike/t458c/smoke.py", "spike/t458c/test_smoke.py", "spike/t458c/README.md")
IMPLEMENTATION = ("Makefile", "cmd/phebs/dev_cohort.go", "cmd/phebs/main.go", "cmd/phebs/serve_config.go")
REUSED = tuple("spike/t503/" + name for name in sorted(shared.HARNESS_FILES | {"qualify.py", "score.py"})) + ("pilot/validation/serve.py", "pilot/validation/receipts.py", "pilot/validation/corpus.py")


def require(condition, message):
    score.require(condition, message)


def readiness(observation, population):
    """Same strict neutral cohort predicates as UI readiness, plus pinned HEADs."""
    expected = {row["repository"]: row for row in population}
    rows = observation["repositories"]
    require(isinstance(rows, list) and all(isinstance(row, dict) for row in rows), "malformed repo-status")
    require(all(row.get("name") in expected for row in rows), "unexpected repository")
    require(len({row["name"] for row in rows}) == len(rows), "duplicate repository")
    for row in rows:
        for name in ("last_index_job", "last_extraction_job", "last_resolver_job", "last_caller_job"):
            job = row.get(name)
            require(job is None or isinstance(job, dict), "malformed job projection")
            require(not job or job.get("status") not in ("failed", "canceled"), "terminal neutral job failure")
        if row.get("indexed_commit_hash") and row["indexed_commit_hash"] != expected[row["name"]]["commit"]:
            raise ValueError("indexed HEAD differs from retained source")
    if len(rows) != 2:
        return False
    for row in rows:
        for kind in ("index", "extraction"):
            if row.get("last_" + kind + "_job_state") != "exact" or (row.get("last_" + kind + "_job") or {}).get("status") != "done":
                return False
        if row.get("indexed_commit_hash") != expected[row["name"]]["commit"]:
            return False
        require(row.get("indexed_analysis_unit") is None, "whole demo carries a focused unit")
        for name in ("last_resolver_job", "last_caller_job"):
            if row.get(name) and row[name].get("status") != "done":
                return False
    for selected in population:
        inventory = observation.get(selected["name"] + "Services")
        if inventory is None:
            return False
        require(inventory.get("schema") == "phebs-service-inventory-v1" and inventory.get("repository", {}).get("repository") == selected["repository"] and inventory["repository"].get("catalog_service_count") == 5 and isinstance(inventory.get("services"), list), "malformed service inventory")
        repository = inventory["repository"]
        authority = repository.get("authority", {})
        require(repository.get("source_commit") == selected["commit"] and authority.get("kind") == "operator" and authority.get("id") == selected["catalog_id"] and authority.get("version") == selected["catalog_version"], "service inventory source/catalog authority differs")
        current = {row["key"] for row in inventory["services"] if row.get("disposition") == "accepted" and row.get("status") == "current" and row.get("removed") is False}
        if not set(selected["required_services"]) <= current:
            return False
    page = observation.get("relationships")
    if page is None:
        return False
    require(page.get("schema") == "phebs-service-relationship-page-v1" and page.get("query", {}).get("repositories") == [population[0]["repository"]] and page["query"].get("service_key") == "orders-api" and page["query"].get("view") == "all" and isinstance(page.get("roots"), list) and isinstance(page.get("rows"), list), "malformed relationship page")
    coverage = page.get("coverage", {})
    ready = (page.get("rows_state") == "nonempty" and len(page["roots"]) == 1 and bool(page["rows"]) and coverage.get("authorized_repositories") == 1 and coverage.get("complete_roots") == 1 and coverage.get("failed_roots") == 0 and coverage.get("unavailable_roots") == 0 and coverage.get("truncated") is False)
    if ready:
        root = page["roots"][0]
        require(root.get("state") == "complete" and root.get("repository") == population[0]["repository"] and root.get("service_key") == "orders-api" and all(root.get(key) for key in ("root_schema", "generation", "root_digest", "authority_digest")), "crossed or incomplete relationship root")
        require(all(row.get("repository") == root["repository"] and row.get("service_key") == "orders-api" for row in page["rows"]), "relationship rows crossed selected service")
    return ready


def check_search(value, selected, service=True, path="service/orders/service.go"):
    receipt = value.get("scope_receipt", {})
    files = value.get("files")
    require(receipt.get("schema") == "phebs-search-scope-v1" and receipt.get("kind") == ("service" if service else "all_code"), "wrong search scope")
    require(isinstance(files, list) and any(row.get("path") == path for row in files), "expected source missing from search")
    require(all(row.get("repo") == selected["repository"] and row.get("ref") == selected["commit"] for row in files), "search emitted crossed source")
    require(receipt.get("revisions") == [{"repository": selected["repository"], "commit": selected["commit"]}], "search revision receipt differs")
    if service:
        authority = receipt.get("service_authority", {})
        require(receipt.get("repository") == selected["repository"] and receipt.get("service_key") == "orders-api" and receipt.get("service_status") == "current", "service search widened or is not current")
        require(authority.get("repository") == selected["repository"] and authority.get("service_key") == "orders-api" and authority.get("revision_commit") == selected["commit"] and all(re.fullmatch(r"sha256:[0-9a-f]{64}", authority.get(key, "")) for key in ("repository_source_generation", "repository_search_generation", "active_source_generation", "digest")), "service search lacks exact whole authority")
    else:
        require(not receipt.get("service_authority"), "All code acquired service authority")


def check_citation(value, page, row, selected):
    root = page["roots"][0]
    require(value.get("schema") == "phebs-service-relationship-citation-v1" and value.get("repository") == selected["repository"], "citation schema/repository differs")
    require(all(value.get(key) == root.get(key) and bool(value.get(key)) for key in ("root_schema", "generation", "root_digest", "authority_digest")), "citation crossed relationship generation")
    require(value.get("evidence") == row.get("evidence") and value.get("projection", {}).get("digest") == row.get("projection_digest"), "citation crossed projection/evidence")
    evidence = value["evidence"]
    path = REPO / "docs/fixtures/t30.7-neutral-service/repo" / evidence["path"]
    require(path.is_relative_to(REPO / "docs/fixtures/t30.7-neutral-service/repo") and ".." not in Path(evidence["path"]).parts and path.is_file(), "citation left retained source")
    blob = path.read_bytes()
    oid = hashlib.sha1(b"blob " + str(len(blob)).encode() + b"\0" + blob).hexdigest()
    require(evidence.get("object_id") == oid and evidence.get("content_digest") == "sha256:" + hashlib.sha256(blob).hexdigest(), "citation immutable source differs")
    start, end = evidence["span"]["start_byte"], evidence["span"]["end_byte"]
    require(isinstance(start, int) and isinstance(end, int) and 0 <= start < end <= len(blob) and value.get("content", "").encode() == blob[start:end], "citation exact span differs")
    return {"path": evidence["path"], "object_id": oid, "content_digest": evidence["content_digest"], "start_byte": start, "end_byte": end, "root_digest": root["root_digest"]}


class Smoke:
    def __init__(self, root, binding, plan):
        self.root, self.binding, self.plan = root, binding, plan
        self.deadline = checks.Deadline(plan["bounds"]["total_seconds"])
        self.session, self.monitor, self.server_groups_drained = None, None, True
        self.report = {"schema": "phebs-t458c-neutral-whole-smoke-v1", "outcome": "preparing", "http_calls": 0, "health_reads": [], "reads": [], "phases": [], "claim": "Bounded neutral integration only; no focused service support, accuracy, scale, managed provider or production release claim."}
        self.raw = root / "private-raw"
        self.raw_bytes = 0
        self.config_path = root / "config.json"

    def prepare(self):
        root, binding, plan = self.root, self.binding, self.plan
        self.raw.mkdir()
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0)); port = sock.getsockname()[1]
        self.base = "http://127.0.0.1:" + str(port)
        self.token = secrets.token_hex(24)
        # The source/catalog connections and whole selection MUST come from
        # the production demo environment bindings, not a parallel config.
        self.config_path.write_text(json.dumps({"server": {"addr": "127.0.0.1:" + str(port), "data_dir": str(root / "data")}, "auth": {"api_key": self.token}, "sync": {"poll_interval": "1s", "resync_interval": "1h"}}))
        self.config_path.chmod(0o600)
        self.env = {key: value for key, value in os.environ.items() if not key.startswith(("PHEBS_", "GIT_"))}
        self.env.update({"PHEBS_DEV_COHORT": "whole", "GOMAXPROCS": "2", "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull, "GIT_ALLOW_PROTOCOL": "file"})
        for key, relative in plan["demo_environment"].items():
            self.env[key] = str(REPO / relative)
        for name, key in (("PHEBS_ZOEKT_GIT_INDEX", "zoekt_git_index"), ("PHEBS_FOCUSED_INDEX", "focused_index"), ("PHEBS_BUF", "buf"), ("PHEBS_SURREAL", "surreal")):
            self.env[name] = binding["tools"][key]["path"]
        self.env["PATH"] = os.pathsep.join([str(Path(binding["tools"][key]["path"]).parent) for key in ("git", "surreal")] + [self.env.get("PATH", "")])
        self.population = [dict(row, repository="local" + str(REPO / row["bundle"])) for row in plan["population"]]

    def work_timeout(self, maximum):
        remaining = self.deadline.remaining() - self.plan["bounds"]["cleanup_reserve_seconds"]
        require(remaining > 0, "smoke work deadline reached; cleanup reserve retained")
        return min(maximum, remaining)

    def reserve_http_call(self):
        calls = self.report["http_calls"]
        require(calls < self.plan["bounds"]["http_calls"], "HTTP call bound exceeded")
        self.report["http_calls"] = calls + 1

    def get(self, path, params=None, authenticated=True):
        bounds = self.plan["bounds"]
        require(self.raw_bytes + bounds["response_bytes"] <= bounds["private_raw_bytes"], "private raw cannot reserve the next bounded response")
        timeout = self.work_timeout(bounds["request_seconds"])
        self.reserve_http_call()
        before = time.monotonic()
        status, raw, _ = shared.http(self.base, path, params, self.token if authenticated else None, timeout, bounds["response_bytes"])
        require(self.raw_bytes + len(raw) <= bounds["private_raw_bytes"], "private raw aggregate bound exceeded")
        name = "response-%04d.json" % (len(self.report["reads"]) + 1)
        (self.raw / name).write_bytes(raw); self.raw_bytes += len(raw)
        self.report["reads"].append({"path": path, "status": status, "bytes": len(raw), "seconds": round(time.monotonic() - before, 6), "raw_digest": checks.sha(self.raw / name)})
        return status, json.loads(raw)

    def record_terminal(self, phase, session, terminal):
        shared.Matrix.record_terminal(self, phase, session, terminal)

    def readiness_health(self, base, path, params=None, token=None, timeout=60):
        remaining = self.plan["bounds"]["startup_seconds"] - (time.monotonic() - self.session.clock)
        require(remaining > 0, "HTTP readiness deadline exceeded")
        timeout = self.work_timeout(min(timeout, remaining))
        self.reserve_http_call()
        before = time.monotonic()
        observation = {"path": path}
        try:
            status, raw, _ = shared.http(base, path, params, token, timeout, self.plan["bounds"]["response_bytes"])
            observation.update(status=status, bytes=len(raw))
            return status, raw
        except BaseException as error:
            observation["error_type"] = type(error).__name__
            raise
        finally:
            observation["seconds"] = round(time.monotonic() - before, 6)
            self.report["health_reads"].append(observation)

    def start(self):
        phase = {"phase": "neutral-whole", "ready_seconds": None}; self.report["phases"].append(phase)
        self.session = shared.OwnedServeSession(self.root / "harness", "neutral-whole", [self.binding["binary"]["path"], "serve", "-config", str(self.config_path)], self.base, env=self.env, ready_timeout_s=self.work_timeout(self.plan["bounds"]["startup_seconds"]), stop_timeout_s=8)
        self.server_groups_drained = False
        original = shared.serve.http_get
        shared.serve.http_get = self.readiness_health
        try:
            self.session.start()
        except BaseException:
            terminal_path = self.root / "harness" / shared.serve.receipts.RECEIPTS_DIR / "neutral-whole.json"
            if terminal_path.exists():
                self.record_terminal(phase, self.session, checks.bounded_json(terminal_path, 65536))
                self.session = None
            raise
        finally:
            shared.serve.http_get = original
        phase["ready_seconds"] = self.session.ready_seconds
        require(self.session.ready_seconds <= self.plan["bounds"]["startup_seconds"], "successful startup crossed deadline")

    def run(self):
        self.start()
        limit = time.monotonic() + self.work_timeout(self.plan["bounds"]["publication_seconds"])
        observation = None
        while time.monotonic() < limit:
            status, rows = self.get("/api/repo-status")
            require(status == 200, "repo-status refused")
            observation = {"repositories": rows}
            for selected in self.population:
                status, value = self.get("/api/services", {"repository": selected["repository"], "page_size": "100"})
                require(status in (200, 404, 409, 503), "unexpected service inventory status")
                if status == 200:
                    observation[selected["name"] + "Services"] = value
            status, value = self.get("/api/service-relationships", {"repository": self.population[0]["repository"], "service_key": "orders-api", "view": "all", "page_size": "100"})
            require(status in (200, 404, 409, 503), "unexpected relationship readiness status")
            if status == 200:
                observation["relationships"] = value
            if readiness(observation, self.population):
                require(time.monotonic() <= limit, "successful readiness crossed publication deadline")
                break
            time.sleep(min(self.plan["bounds"]["poll_seconds"], self.work_timeout(1)))
        else:
            raise ValueError("strict cohort publication deadline exceeded")
        self.report["readiness"] = observation
        selected = self.population[0]
        params = {"q": "T307_FOCUSED_SERVICE_NEEDLE", "scope": "service", "repository": selected["repository"], "service_key": "orders-api"}
        require(self.get("/api/search", params, authenticated=False)[0] == 401, "anonymous service search admitted")
        status, value = self.get("/api/search", params)
        require(status == 200, "exact service search refused")
        check_search(value, selected); self.report["service_search"] = value["scope_receipt"]
        status, value = self.get("/api/search", {"q": "T307_OUTSIDE_UNIT_BULK_NEEDLE repo:^" + re.escape(selected["repository"]) + "$", "scope": "all_code"})
        require(status == 200, "whole sentinel search refused")
        check_search(value, selected, service=False, path="bulk/irrelevant.txt")
        self.report["whole_sentinel"] = value["scope_receipt"]
        page = observation["relationships"]
        row = page["rows"][0]
        require(bool(row.get("citation")), "relationship omitted citation")
        status, value = self.get("/api/service-relationship-citation", {"citation": row["citation"]})
        require(status == 200, "relationship citation refused")
        self.report["relationship_citation"] = check_citation(value, page, row, selected)

    def stop(self):
        if self.session is not None and self.session.pid is not None:
            session = self.session
            session.owned_pgid = session.pid
            remaining = self.deadline.limit - self.deadline.clock()
            # Leave the existing helper's five-second reap and two-second
            # group-drain tail inside the same global cleanup budget.
            session.stop_timeout_s = min(8, max(0, remaining - 7))
            terminal = session.stop("graceful")
            self.record_terminal(self.report["phases"][-1], session, terminal)
            self.session = None
            require(terminal["outcome"] == "success" and terminal["teardown"] == "clean", "server teardown failed or required forced cleanup")
        self.deadline.remaining()

    def cleanup(self, failed):
        shared.require_derived_cleanup(self)
        self.config_path.unlink(missing_ok=True)
        for entry in self.root.iterdir():
            if failed and entry == self.raw:
                continue
            if entry.is_dir():
                shutil.rmtree(entry)
            else:
                entry.unlink()
        if failed:
            (self.root / "stop-diagnostic.json").write_text(json.dumps(self.report, indent=2) + "\n")
            self.report["retained_stop_root"] = str(self.root)
        else:
            self.root.rmdir()
        self.deadline.remaining()


def validate_binding(binding, plan):
    require(binding["schema"] == "phebs-t458c-execution-binding-v1" and binding["reviewed"] is True and binding["plan_digest"] == checks.sha(HERE / "preregistration.json"), "unreviewed smoke binding")
    require(set(binding["harness_files"]) == set(OWNED + REUSED) and set(binding["implementation_files"]) == set(IMPLEMENTATION), "smoke source inventory differs")
    for group in (binding["harness_files"], binding["implementation_files"], plan["protected_files"]):
        for relative, expected in group.items():
            require(checks.sha(REPO / relative) == expected, "bound source/input drift: " + relative)
    for item in [binding["binary"], *binding["tools"].values()]:
        path = Path(item["path"])
        require(path.is_absolute() and path.is_file() and checks.sha(path) == item["sha256"] and path.stat().st_size == item["bytes"], "bound image/tool drift")
    require(set(binding["tools"]) == {"go", "git", "surreal", "buf", "zoekt_git_index", "focused_index"}, "smoke tool census differs")
    metadata = subprocess.check_output([binding["tools"]["go"]["path"], "version", "-m", binding["binary"]["path"]], timeout=5, text=True)
    require("vcs.revision=" + binding["source_commit"] in metadata and "vcs.modified=false" in metadata and "sha256:" + hashlib.sha256(metadata.encode()).hexdigest() == binding["binary"]["metadata_sha256"], "binary is not exact clean source")
    require(subprocess.check_output([binding["tools"]["git"]["path"], "rev-parse", "HEAD"], cwd=REPO, timeout=5, text=True).strip() == binding["harness_commit"] and not subprocess.check_output([binding["tools"]["git"]["path"], "status", "--porcelain", "--untracked-files=no"], cwd=REPO, timeout=5, text=True).strip(), "harness is not the clean reviewed commit")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binding", type=Path, required=True)
    parser.add_argument("--run-root", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    plan = checks.bounded_json(HERE / "preregistration.json", 65536)
    binding = checks.bounded_json(args.binding, 65536)
    require(not args.run_root.exists() and not args.out.exists() and args.run_root.is_absolute() and args.out.is_absolute() and not args.out.is_relative_to(args.run_root), "fresh private scratch/output required")
    try:
        validate_binding(binding, plan)  # Source/tool admission, before product work.
    except BaseException as error:
        value = {"schema": "phebs-t458c-neutral-whole-smoke-v1", "outcome": "stopped", "phase": "source_tool_admission", "error_type": type(error).__name__, "error": str(error), "product_started": False, "execution_binding_digest": checks.sha(args.binding), "plan_digest": checks.sha(HERE / "preregistration.json")}
        with os.fdopen(os.open(args.out, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w") as output:
            json.dump(value, output, indent=2); output.write("\n")
        return 1
    args.run_root.mkdir(mode=0o700)
    smoke = Smoke(args.run_root, binding, plan)
    failure = None
    try:
        smoke.prepare()
        smoke.run()
        smoke.report["outcome"] = "passed"
    except BaseException as error:
        failure = error
        smoke.report.update(outcome="stopped", error_type=type(error).__name__, error=str(error))
    finally:
        try:
            smoke.stop()
        except BaseException as error:
            failure = failure or error
            smoke.report.update(outcome="stopped", cleanup_error_type=type(error).__name__)
        try:
            smoke.cleanup(failure is not None)
        except BaseException as error:
            failure = failure or error
            smoke.report.update(outcome="stopped", cleanup_failure_type=type(error).__name__)
            if args.run_root.exists():
                smoke.report["retained_stop_root"] = str(args.run_root)
        smoke.report.update(total_seconds=round(smoke.deadline.elapsed(), 6), execution_binding_digest=checks.sha(args.binding), plan_digest=checks.sha(HERE / "preregistration.json"))
        with os.fdopen(os.open(args.out, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w") as output:
            json.dump(smoke.report, output, indent=2); output.write("\n")
    return 1 if failure else 0


if __name__ == "__main__":
    raise SystemExit(main())
