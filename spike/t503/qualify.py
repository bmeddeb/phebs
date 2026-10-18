#!/usr/bin/env python3
"""One finite T50.3 real-source baseline through the ordinary signed engine."""

import argparse
from collections import Counter
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
import threading
import time

import score

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
sys.path.insert(0, str(REPO / "pilot/validation"))
import serve  # noqa: E402


def digest(path):
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


def command(argv, **kwargs):
    return subprocess.check_output([str(value) for value in argv], timeout=30, text=True, **kwargs)


def physical_headroom():
    raw = command(["/usr/bin/vm_stat"])
    size = int(re.search(r"page size of (\d+) bytes", raw).group(1))
    counts = {name: int(re.search(r"Pages " + name + r":\s+(\d+)\.", raw).group(1)) for name in ("free", "inactive", "speculative")}
    return {"available_physical_bytes": size * sum(counts.values()), "page_size": size, "pages": counts,
            "formula": "page_size*(free+inactive+speculative); compressor and swap excluded"}


def allocated_bytes(root):
    total = 0
    for path, directories, files in os.walk(root, followlinks=False):
        for name in directories + files:
            entry = Path(path) / name
            if entry.is_symlink():
                raise ValueError("derived data measurement encountered a symlink")
            total += entry.stat().st_blocks * 512
    return total


class Monitor:
    def __init__(self, observer, pid, data, bounds, directory):
        self.pid, self.data, self.bounds = pid, data, bounds
        self.samples, self.headroom, self.disk = [], [], []
        self.failure = None
        self.done = threading.Event()
        self.stderr = (directory / "observer.stderr").open("w")
        self.child = subprocess.Popen([str(observer)], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                      stderr=self.stderr, text=True)
        self.thread = threading.Thread(target=self.run)
        self.thread.start()

    def run(self):
        try:
            last_host = 0
            while not self.done.is_set():
                self.child.stdin.write(str(self.pid) + "\n")
                self.child.stdin.flush()
                import select
                if not select.select([self.child.stdout], [], [], 2)[0]:
                    raise ValueError("native process measurement unavailable")
                sample = json.loads(self.child.stdout.readline())
                if sample.get("unavailable") or sample.get("rss_bytes", 0) <= 0:
                    raise ValueError("native process measurement unavailable")
                if sample["rss_bytes"] > self.bounds["process_tree_sampled_max_rss_bytes"]:
                    raise ValueError("sampled RSS bound exceeded")
                self.samples.append(sample)
                if time.monotonic() - last_host >= 1:
                    host = physical_headroom()
                    if host["available_physical_bytes"] < self.bounds["minimum_available_physical_headroom_bytes"]:
                        raise ValueError("actual physical headroom below frozen bound")
                    self.headroom.append(host)
                    size = allocated_bytes(self.data)
                    if size > self.bounds["derived_allocated_disk_bytes"]:
                        raise ValueError("observed allocated derived data exceeds frozen bound")
                    self.disk.append(size)
                    last_host = time.monotonic()
                self.done.wait(0.2)
        except BaseException as error:
            self.failure = str(error)
            self.done.set()

    def check(self):
        if self.failure:
            raise ValueError(self.failure)

    def stop(self):
        self.done.set()
        self.thread.join(timeout=4)
        if self.thread.is_alive():
            self.child.kill()
            self.thread.join(timeout=2)
            self.failure = self.failure or "observer did not join"
        self.child.stdin.close()
        self.child.wait(timeout=3)
        self.stderr.close()
        self.check()
        if not self.samples or not self.headroom or not self.disk:
            raise ValueError("required operating measurement absent")
        return {"rss_samples": len(self.samples), "sampled_max_tree_rss_bytes": max(s["rss_bytes"] for s in self.samples),
                "sampled_max_descendants": max(s["descendants"] for s in self.samples),
                "minimum_available_physical_bytes": min(s["available_physical_bytes"] for s in self.headroom),
                "physical_samples": len(self.headroom), "physical_formula": self.headroom[0]["formula"],
                "sampled_max_data_allocated_bytes": max(self.disk), "rss_cadence_seconds": 0.2,
                "host_disk_cadence_seconds": 1,
                "qualification": "Sequential native samples after HTTP readiness; not an atomic tree snapshot, transient peak or hard enforcement. Startup before HTTP readiness and teardown are not RSS-sampled. No swap credit."}


def query(labels, repository, level):
    result = {"level": level}
    for side in ("old", "replacement"):
        endpoint = labels["endpoints"][side]
        lineage = "provisional_repo_path_v1_" + hashlib.sha256((repository + "\0" + endpoint["declaration_path"]).encode()).hexdigest()
        result.update({side + "_protocol": "protobuf", side + "_repository": repository,
                       side + "_lineage": lineage, side + "_operation": endpoint["wire_operation"]})
    return result


def mcp_arguments(params):
    return {key.replace("_lineage", "_declaration_lineage"): value for key, value in params.items()}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--observer", type=Path, required=True)
    parser.add_argument("--run-root", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    root = args.run_root
    score.require(not (root / "baseline-started").exists(), "one baseline attempt already started; no silent retry")
    labels = json.loads((HERE / "labels.json").read_text())
    binding = json.loads((HERE / "committed-inputs.json").read_text())
    prereg = json.loads((HERE / "preregistration.json").read_text())
    bounds = prereg["operating"]["bounds"]
    score.require(digest(HERE / "checkpoint.json") == "sha256:54b1377c20d2422f93756dedea56ef30aa1df0e6114d41fd646558ef4cd3ab29", "checkpoint drift")
    score.require(digest(REPO / "spike/t503/inputs-bundles/base.bundle") == "sha256:" + binding["bundle"]["sha256"], "bundle drift")
    metadata = command(["go", "version", "-m", args.binary])
    binary_commit = re.search(r"vcs.revision=([0-9a-f]{40})", metadata).group(1)
    score.require("vcs.modified=false" in metadata, "unclean product binary")
    for role in binding["roles"]:
        actual = command(["git", "rev-parse", "HEAD"], cwd=role["local_path"]).strip()
        score.require(actual == binding["derived_input_commit"], "evaluation role moved before baseline")
    artifacts, selection = root / "artifacts", root / "selection"
    artifacts.mkdir(); selection.mkdir()
    for path in (REPO / "spike/t475/release/artifacts").iterdir():
        shutil.copyfile(path, artifacts / path.name)
    comparison = "caller-comparison"
    (artifacts / (comparison + "-card-1.0.0.md")).write_text("# Disposable T50.3 integration selection\n\nActual source-quality measurement is in progress. This card grants no production release or accuracy result. The integration key is disposable.\n")
    (artifacts / (comparison + "-manifest-1.0.0.json")).write_text(json.dumps({"schema": "phebs-t503-integration-manifest-v1", "pack_id": "phebs.grpc.caller.comparison", "implementation": "compare_operation_callers@caller-comparison-v2", "production_release": False}))
    (artifacts / (comparison + "-validation-1.0.0.json")).write_text(json.dumps({"schema": "phebs-t503-integration-validation-v1", "qualification": "preregistered_measurement_in_progress_no_quality_result", "checkpoint": digest(HERE / "checkpoint.json"), "production_release": False}))
    template = json.loads((REPO / "spike/t475/release/receipts/template.json").read_text())
    private = root / "integration-only.pem"
    command(["/opt/homebrew/opt/openssl@3/bin/openssl", "genpkey", "-algorithm", "ED25519", "-out", private])
    private.chmod(0o600)
    public = None
    try:
        for pack, stem in (("phebs.grpc.caller.go", "caller-map"), ("phebs.grpc.caller.comparison", comparison)):
            current = json.loads(json.dumps(template))
            current.update(pack_id=pack, release_id="rel-" + pack + "-1.0.0", approval_records=["t503-disposable-key-qualification-only"], approved_at="2026-10-18T00:00:00Z")
            for key in ("card", "manifest", "validation"):
                current[key]["artifact_id"] = stem + "-" + key + "-1.0.0" + (".md" if key == "card" else ".json")
            path = root / (stem + "-template.json")
            path.write_text(json.dumps(current))
            output = command([args.binary, "release-sign", "-template", path, "-key", private, "-key-id", "t503-integration-only", "-artifacts", artifacts, "-out", selection / (stem + ".json")])
            value = next(line.removeprefix("public key ") for line in output.splitlines() if line.startswith("public key "))
            score.require(public in (None, value), "test public key changed")
            public = value
    finally:
        private.unlink(missing_ok=True)
    rewrite = root / "gitconfig"
    rewrite.write_text("\n".join(f'[url "file://{role["local_path"]}"]\n\tinsteadOf = https://{role["repository"]}.git\n' for role in binding["roles"]))
    rewrite.chmod(0o600)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0)); port = sock.getsockname()[1]
    base, token = "http://127.0.0.1:" + str(port), secrets.token_hex(24)
    config = {"server": {"addr": "127.0.0.1:" + str(port), "data_dir": str(root / "data")},
              "auth": {"api_key": token, "cookie_secure": False},
              "sync": {"poll_interval": "1s", "resync_interval": "1h"},
              "webhook": {"secret": secrets.token_hex(24)},
              "connections": [{"name": role["role"].replace("_", "-"), "type": "git", "url": "https://" + role["repository"] + ".git"} for role in binding["roles"]],
              "release_selection": {"path": str(selection), "artifacts_path": str(artifacts), "keys": [{"id": "t503-integration-only", "public_key": public}]}}
    cfg = root / "config.json"; cfg.write_text(json.dumps(config)); cfg.chmod(0o600)
    env = {key: value for key, value in os.environ.items() if not key.startswith("PHEBS_")}
    helpers = Path("/Users/ben/.codex/worktrees/t445-header/phebs.com/bin")
    env.update({"PHEBS_ZOEKT_GIT_INDEX": str(helpers / "zoekt-git-index"), "PHEBS_FOCUSED_INDEX": str(helpers / "phebs-focused-index"),
                "PHEBS_BUF": str(helpers / "buf"), "PHEBS_T472_SOURCE_OUTCOME_ROOT": str(root / "source-outcomes"),
                "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": str(rewrite), "GIT_ALLOW_PROTOCOL": "file", "GOMAXPROCS": "2"})
    preflight = physical_headroom()
    score.require(preflight["available_physical_bytes"] >= bounds["minimum_available_physical_headroom_bytes"], "physical headroom below frozen bound")
    score.require(shutil.disk_usage(root).free >= bounds["minimum_free_disk_bytes"], "free disk below frozen bound")
    started = time.monotonic()
    (root / "baseline-started").write_text("one frozen baseline attempt\n")
    report = {"schema": "phebs-t503-public-source-baseline-v1", "recorded_date": "2026-10-18", "outcome": "running", "source_commit": binary_commit,
              "binary_digest": digest(args.binary), "checkpoint_digest": digest(HERE / "checkpoint.json"), "labels_digest": digest(HERE / "labels.json"),
              "committed_inputs_digest": digest(HERE / "committed-inputs.json"), "evaluation_commit": binding["derived_input_commit"],
              "production_signing": "pending; existing key never accessed; disposable integration key removed before launch", "headroom_preflight": preflight,
              "qualification": "Real public-source composition census through ordinary signed engine; selected endpoint roles and supplied package attribution, no upstream migration/deployment/scale inference.", "reads": []}
    session, monitor = None, None
    raw_directory = root / "raw"; raw_directory.mkdir()
    sequence = 0
    try:
        session = serve.ServeSession(root / "harness", "baseline", [str(args.binary), "serve", "-config", str(cfg)], base, env=env, ready_timeout_s=bounds["startup_seconds"], stop_timeout_s=15)
        session.start()
        report["ready_seconds"] = session.ready_seconds
        monitor = Monitor(args.observer, session.pid, root / "data", bounds, root)

        def get(path, params=None, maximum=None, authenticated=True):
            nonlocal sequence
            monitor.check()
            score.require(time.monotonic() - started <= bounds["complete_native_seconds"], "finite native wall bound exceeded")
            clock = time.monotonic()
            status, body = serve.http_get(base, path, params, token if authenticated else None, timeout=5)
            seconds = time.monotonic() - clock
            score.require(len(body) <= bounds["http_response_bytes"], "HTTP response byte bound exceeded")
            if maximum is not None:
                score.require(seconds <= maximum, "comparison read latency bound exceeded")
            report["reads"].append({"path": path, "status": status, "seconds": round(seconds, 6), "bytes": len(body)})
            sequence += 1
            (raw_directory / f"http-{sequence:03}.json").write_bytes(body)
            return status, body

        repository = binding["roles"][0]["repository"]
        waiting = query(labels, repository, "occurrence"); waiting["page_size"] = 100
        deadline = time.monotonic() + bounds["publication_seconds"]
        while True:
            status, body = get("/api/compare_operation_callers", waiting)
            page = json.loads(body) if status == 200 else {}
            if page.get("matching_rows_state") == "exact":
                break
            score.require(time.monotonic() <= deadline, "comparison publication deadline exceeded")
            time.sleep(0.5)
        score.require(report["reads"][-1]["seconds"] <= bounds["cold_request_seconds"], "cold comparison read latency bound exceeded")
        report["cold_first_occurrence_seconds"] = report["reads"][-1]["seconds"]
        report["publication_seconds"] = round(time.monotonic() - started, 3)
        quality = {}
        for level in ("occurrence", "unit"):
            params = query(labels, repository, level)
            first_rows = None
            for size in (1, 2, 100):
                pages, cursor, seen = [], "", set()
                for _ in range(6):
                    current = {**params, "page_size": size}
                    if cursor:
                        current["cursor"] = cursor
                    status, body = get("/api/compare_operation_callers", current, bounds["warm_request_seconds"] if pages else bounds["cold_request_seconds"])
                    score.require(status == 200, "comparison page refused")
                    value = json.loads(body); pages.append(value)
                    if value["pagination"]["complete"]:
                        break
                    cursor = value["pagination"]["next_cursor"]
                    score.require(cursor and cursor not in seen, "comparison cursor repeats")
                    seen.add(cursor)
                else:
                    raise ValueError("comparison pagination exceeded frozen finite census")
                rows, citations = score.check_rows(pages, labels, binding, level, repository)
                if first_rows is None:
                    first_rows = rows
                    for key, citation in citations.items():
                        status, citation_raw = get("/api/contract_callers/citation", {"citation": citation})
                        score.require(status == 200, "exact public source citation refused")
                        source = json.loads(citation_raw)
                        truth = next(row for row in labels["sites"] if row["site_id"] == key)
                        score.require(source["content"] == truth["selector"] and score.site_id(source["source"]) == key, "citation bytes disagree with frozen source")
                    quality[level] = {"rows": len(rows), "classes": dict(Counter(row["classification"] for row in rows)), "selected_sites": len(citations), "page_sizes": [1, 2, 100]}
                for _ in range(5 if size == 100 else 0):
                    status, warm_raw = get("/api/compare_operation_callers", {**params, "page_size": 100}, bounds["warm_request_seconds"])
                    score.require(status == 200, "warm comparison read refused")
                    score.check_rows([json.loads(warm_raw)], labels, binding, level, repository)
                if size == 100:
                    mcp = serve.mcp_call(base, "compare_operation_callers", mcp_arguments({**params, "page_size": 100}), token=token, timeout=5)
                    (raw_directory / ("mcp-" + level + ".json")).write_text(json.dumps(mcp))
                    serve.parity(body, mcp, frozenset({"$schema", "citation", "next_cursor"}))
            score.require(first_rows is not None, "missing comparison census")
        score.require(get("/api/compare_operation_callers", waiting, authenticated=False)[0] == 401, "anonymous comparison admitted")
        capabilities = json.loads(get("/api/version")[1])["capabilities"]
        score.require(all(name in capabilities for name in ("contract-caller-map", "contract-caller-comparison")), "ordinary signed products absent")
        score.require(not any(name in capabilities for name in ("contract-impact-report", "kafka-topics", "wire-compatibility")), "unrelated product activated")
        report["quality"] = quality
        report["checks"] = {"http_mcp_parity": True, "anonymous_401": True, "ordinary_signed_selection": True,
                            "experimental_switches": False, "private_process_git_transport_mapping": True,
                            "source_outcome_capture": "existing strict T472 capture; external measurement only, no changed facts"}
        report["outcome"] = "source_rows_passed_additional_processing_ui_and_operating_closure_pending"
    except BaseException as error:
        report["outcome"] = "stopped_no_retry"
        report["reason"] = str(error)
        raise
    finally:
        if monitor is not None:
            try:
                report["operating_samples"] = monitor.stop()
            except BaseException as error:
                report["operating_failure"] = str(error)
                report["outcome"] = "stopped_no_retry"
        if session is not None and session.pid is not None:
            terminal = session.stop("graceful", reason="" if report["outcome"].startswith("source_rows_passed") else "T50.3 frozen baseline stopped")
            report["terminal"] = {key: terminal[key] for key in ("outcome", "exit_code", "stop_mode", "teardown", "wall_seconds")}
        report["seconds"] = round(time.monotonic() - started, 3)
        args.out.write_text(json.dumps(report, indent=2) + "\n")
        print(json.dumps({"outcome": report["outcome"], "receipt": str(args.out), "seconds": report["seconds"]}))


if __name__ == "__main__":
    main()
