#!/usr/bin/env python3
"""One reviewed post-T503a matrix; never retries the retained first attempt."""
import argparse
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
import hashlib
import hmac
from http.client import HTTPConnection
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import signal
import socket
import threading
import tempfile
import time
import urllib.parse

import qualify
import requalification_checks as checks
import requalification_browser
import requalification_monitor
import score

HERE, REPO, serve = qualify.HERE, qualify.REPO, qualify.serve
VOLATILE = frozenset({"$schema", "citation", "next_cursor"})
HARNESS_FILES = frozenset({"requalify.py", "requalification_checks.py", "requalification_browser.py", "requalification_browser.cjs", "requalification_monitor.py", "test_requalification.py"})
HELPERS = {"zoekt_git_index": "zoekt-git-index", "focused_index": "phebs-focused-index"}
DARK_CAPABILITIES = frozenset({"contract-impact-report", "protobuf-field-references", "thrift-field-references", "contract-compatibility", "kafka-topic-inventory", "kafka-topic-usage", "wire-compatibility"})
DARK_TOOLS = frozenset({"find_proto_field_references", "find_field_references", "check_contract_compatibility", "find_kafka_topic_usage", "list_kafka_topics", "read_topic_inventory", "check_wire_compatibility"})


class OwnedServeSession(serve.ServeSession):
    """Retain the forked process group before the helper clears its PID."""
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.owned_pgid = None

    def _finish(self, *args, **kwargs):
        self.owned_pgid = self.pid
        return super()._finish(*args, **kwargs)


def require_derived_cleanup(matrix):
    score.require(matrix.server_groups_drained, "server process group not proven drained; derived custody retained")
    score.require(matrix.monitor is None or matrix.monitor.joined, "sampler remains live; derived custody retained")


def select_helpers(binding, directory):
    """An explicit directory may relocate only the already bound helper bytes."""
    for key, filename in HELPERS.items():
        item = binding["tools"][key]
        path = directory.resolve() / filename
        score.require(path.is_file() and checks.sha(path) == item["sha256"], "helper override differs from reviewed image: " + filename)
        item["path"] = str(path)


def verify_input_repository(git, path, commit, tree, files):
    score.require(qualify.command([git, "rev-parse", "HEAD"], cwd=path).strip() == commit, "evaluation role HEAD changed")
    score.require(qualify.command([git, "rev-parse", commit + "^{tree}"], cwd=path).strip() == tree, "evaluation role tree changed")
    score.require(not qualify.command([git, "status", "--porcelain", "--untracked-files=all"], cwd=path).strip(), "evaluation clone is not pristine")
    raw = qualify.command([git, "ls-tree", "-rl", "-z", "--full-tree", commit], cwd=path)
    census = {}
    for record in raw.split("\0"):
        if not record:
            continue
        metadata, name = record.split("\t", 1)
        mode, kind, oid, size = metadata.split()
        score.require(mode == "100644" and kind == "blob", "evaluation tree has non-file source authority")
        census[name] = {"git_blob": oid, "bytes": int(size)}
    expected = {f["path"]: {"git_blob": f["git_blob"], "bytes": f["bytes"]} for f in files}
    score.require(census == expected and len(census) <= 32 and sum(f["bytes"] for f in census.values()) <= 16777216, "evaluation committed file census differs")
    return {"commit": commit, "tree": tree, "files": len(census), "bytes": sum(f["bytes"] for f in census.values()), "census_digest": "sha256:" + hashlib.sha256(json.dumps(census, sort_keys=True, separators=(",", ":")).encode()).hexdigest()}


def http(base, path, params, token, timeout, maximum, payload=None, extra=None):
    target = urllib.parse.urlsplit(base)
    score.require(target.scheme == "http" and target.hostname == "127.0.0.1" and target.port, "HTTP target must be exact local origin")
    url = path + ("?" + urllib.parse.urlencode(params) if params else "")
    headers = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    if extra:
        headers.update(extra)
    connection = HTTPConnection(target.hostname, target.port, timeout=timeout)
    owned_socket, guard = None, threading.Lock()
    expired = threading.Event()
    def expire():
        expired.set()
        with guard:
            if owned_socket is not None:
                try:
                    owned_socket.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
    clock = time.monotonic()
    timer = threading.Timer(timeout, expire)
    timer.start()
    try:
        connection.connect()
        with guard:
            owned_socket = connection.sock
        score.require(not expired.is_set(), "HTTP total header/body deadline exceeded")
        connection.request("POST" if payload is not None else "GET", url, body=payload, headers=headers)
        with connection.getresponse() as response:
            raw = response.read(maximum + 1)
            score.require(not expired.is_set() and time.monotonic() - clock <= timeout, "HTTP total header/body deadline exceeded")
            score.require(len(raw) <= maximum, "HTTP/MCP response byte bound exceeded")
            return response.status, raw, response.getheader("Content-Type", "")
    finally:
        timer.cancel()
        connection.close()
        timer.join()


def rpc_result(raw, kind):
    if kind.startswith("text/event-stream"):
        events = [line[6:] for line in raw.splitlines() if line.startswith(b"data: ")]
        score.require(len(events) == 1, "MCP response must contain exactly one event")
        raw = events[0]
    message = json.loads(raw)
    score.require(message.get("id") == 1 and "result" in message, "MCP JSON-RPC refused the request")
    return message["result"]


def inventory_digest(root, folders):
    rows = []
    for folder in folders:
        selected = root / folder
        paths = sorted(selected.rglob("*")) if selected.is_dir() else [selected]
        for path in paths:
            if path.is_file():
                score.require(not path.is_symlink(), "linked UI input")
                rows.append({"path": path.relative_to(root).as_posix(), "sha256": checks.sha(path)})
    return "sha256:" + hashlib.sha256(json.dumps(rows, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def validate_binding(binding, plan):
    score.require(binding["schema"] == "phebs-t503-requalification-binding-v1", "unknown execution binding")
    score.require(binding["reviewed"] is True and binding["plan_digest"] == checks.sha(HERE / "requalification-plan.json"), "execution binding lacks exact plan review")
    commit = binding["source_commit"]
    score.require(re.fullmatch(r"[0-9a-f]{40}", commit) and re.fullmatch(r"[0-9a-f]{40}", binding["source_tree"]), "implementation commit/tree is pending")
    score.require(qualify.command(["git", "rev-parse", "HEAD"], cwd=REPO).strip() == binding["harness_commit"], "harness commit differs from reviewed source")
    score.require(not qualify.command(["git", "status", "--porcelain", "--untracked-files=no"], cwd=REPO).strip(), "tracked source is dirty")
    qualify.command(["git", "merge-base", "--is-ancestor", plan["reason"]["repair_checkpoint"], commit], cwd=REPO)
    score.require(set(binding["harness_files"]) == HARNESS_FILES, "execution binding omits reviewed harness files")
    for name, expected in binding["harness_files"].items():
        score.require(checks.sha(HERE / name) == expected, "reviewed harness changed: " + name)
    for item in [binding["binary"], binding["observer"], *binding["tools"].values()]:
        path = Path(item["path"])
        score.require(path.is_absolute() and path.is_file() and checks.sha(path) == item["sha256"], "executed image/tool digest differs")
    binary = Path(binding["binary"]["path"])
    score.require(binary.stat().st_size == binding["binary"]["bytes"], "binary size differs")
    metadata = qualify.command([binding["tools"]["go"]["path"], "version", "-m", binary])
    score.require("vcs.revision=" + commit in metadata and "vcs.modified=false" in metadata, "binary VCS metadata differs or is dirty")
    score.require("sha256:" + hashlib.sha256(metadata.encode()).hexdigest() == binding["binary"]["metadata_sha256"], "binary metadata digest differs")
    score.require(qualify.command(["git", "rev-parse", commit + "^{tree}"], cwd=REPO).strip() == binding["source_tree"], "implementation tree differs")
    for key in ("build_receipt", "helper_build_receipt"):
        item = binding[key]
        score.require(checks.sha(Path(item["path"])) == item["sha256"], "reviewed build receipt changed")
    built = checks.bounded_json(Path(binding["build_receipt"]["path"]), 65536)
    score.require(built["source_commit"] == commit and built["source_tree"] == binding["source_tree"] and built["source_clean_before"] is True and built["source_clean_after"] is True and built["binary_sha256"] == binding["binary"]["sha256"].removeprefix("sha256:"), "release image preparation differs")
    helpers = checks.bounded_json(Path(binding["helper_build_receipt"]["path"]), 65536)
    module_bytes = qualify.command(["git", "show", commit + ":go.mod"], cwd=REPO).encode()
    score.require(helpers["source_commit"] == commit and helpers["go_mod_sha256"] == hashlib.sha256(module_bytes).hexdigest(), "helper module lineage differs")
    expected_helpers = {filename: binding["tools"][key]["sha256"].removeprefix("sha256:") for key, filename in HELPERS.items()}
    expected_helpers["native-observer"] = binding["observer"]["sha256"].removeprefix("sha256:")
    score.require({row["name"]: row["sha256"] for row in helpers["runs"]} == expected_helpers and all(row["exit"] == 0 and row["joined"] is True for row in helpers["runs"]), "helper preparation differs or was not joined")
    score.require(helpers["observer_source_sha256"] == checks.sha(HERE / "observe/main.go").removeprefix("sha256:"), "observer source differs from frozen first observer")
    score.require(inventory_digest(REPO, ["ui/src", "ui/public", "ui/package.json", "ui/package-lock.json"]) == binding["ui"]["source_inventory_digest"], "embedded UI source changed")
    score.require(inventory_digest(Path(binding["ui"]["dist_workspace"]), ["ui/dist"]) == binding["ui"]["dist_inventory_digest"], "built UI changed")
    preflight = checks.bounded_json(Path(binding["browser_preflight"]["path"]), 65536)
    score.require(checks.sha(Path(binding["browser_preflight"]["path"])) == binding["browser_preflight"]["sha256"] and preflight["outcome"] == "passed", "browser preflight unavailable")
    score.require(preflight["chrome_path"] == str(Path(binding["tools"]["chrome"]["path"]).resolve()) and preflight["playwright_version"] == binding["playwright_version"], "preflight renderer differs")
    return metadata


def selections(root, binding):
    artifacts, selection = root / "artifacts", root / "selection"
    artifacts.mkdir(); selection.mkdir()
    for path in (REPO / "spike/t475/release/artifacts").iterdir():
        shutil.copyfile(path, artifacts / path.name)
    stem = "caller-comparison"
    for kind, extension, content in (
        ("card", ".md", "# Disposable T503 post-fix integration selection\nNo production release or quality pass is granted. Frozen measurement is in progress; integration key only.\n"),
        ("manifest", ".json", json.dumps({"schema": "phebs-t503-integration-manifest-v1", "pack_id": "phebs.grpc.caller.comparison", "implementation": "compare_operation_callers@caller-comparison-v2", "production_release": False})),
        ("validation", ".json", json.dumps({"schema": "phebs-t503-integration-validation-v1", "qualification": "prospective_postfix_measurement_in_progress", "plan_digest": checks.sha(HERE / "requalification-plan.json"), "production_release": False})),
    ):
        (artifacts / (stem + "-" + kind + "-1.0.0" + extension)).write_text(content)
    private = root / "disposable-only.pem"
    openssl = binding["tools"]["openssl"]["path"]
    template = json.loads((REPO / "spike/t475/release/receipts/template.json").read_text())
    public, records = None, []
    try:
        qualify.command([openssl, "genpkey", "-algorithm", "ED25519", "-out", private]); private.chmod(0o600)
        for pack, name in (("phebs.grpc.caller.go", "caller-map"), ("phebs.grpc.caller.comparison", stem)):
            current = json.loads(json.dumps(template))
            current.update(pack_id=pack, release_id="rel-" + pack + "-1.0.0", approval_records=["t503-postfix-disposable-key-measurement-only"], approved_at="2026-10-18T00:00:00Z")
            for kind in ("card", "manifest", "validation"):
                current[kind]["artifact_id"] = name + "-" + kind + "-1.0.0" + (".md" if kind == "card" else ".json")
            path = root / (name + "-template.json"); path.write_text(json.dumps(current))
            output = selection / (name + ".json")
            text = qualify.command([binding["binary"]["path"], "release-sign", "-template", path, "-key", private, "-key-id", "t503-postfix-integration-only", "-artifacts", artifacts, "-out", output])
            key = next(line.removeprefix("public key ") for line in text.splitlines() if line.startswith("public key "))
            score.require(public in (None, key), "integration public key changed")
            public = key
            records.append({"pack": pack, "record_digest": checks.sha(output), "record": json.loads(output.read_text())})
    finally:
        private.unlink(missing_ok=True)
    return artifacts, selection, public, records


class Matrix:
    def __init__(self, root, binding, labels, inputs, transitions, bounds):
        self.root, self.binding, self.labels, self.inputs = root, binding, labels, inputs
        self.transitions, self.bounds = transitions, bounds
        self.deadline, self.session, self.monitor = None, None, None
        self.server_groups_drained = True
        self.lock, self.sequence = threading.Lock(), 0
        self.report = {"schema": "phebs-t503-postfix-matrix-v1", "date": "2026-10-18", "outcome": "preparing", "reads": [], "phases": [], "production_signing": "pending; no production key or production signature"}
        self.raw = root / "private-raw"; self.raw.mkdir()
        self.rewrite = root / "gitconfig"
        self.active_roles = {r["role"]: r for r in inputs["roles"]}
        self.write_rewrite()
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0)); port = sock.getsockname()[1]
        self.base = "http://127.0.0.1:" + str(port)
        self.token, self.webhook_secret = secrets.token_hex(24), secrets.token_hex(24)
        self.config_path = root / "config.json"
        self.config = {"server": {"addr": "127.0.0.1:" + str(port), "data_dir": str(root / "data")}, "auth": {"api_key": self.token, "cookie_secure": False}, "sync": {"poll_interval": "1s", "resync_interval": "1h"}, "webhook": {"secret": self.webhook_secret}, "connections": [{"name": r["role"].replace("_", "-"), "type": "git", "url": "https://" + r["repository"] + ".git"} for r in inputs["roles"]]}
        self.env = {k: v for k, v in os.environ.items() if not k.startswith(("PHEBS_", "GIT_"))}
        tools = binding["tools"]
        self.env.update({"PHEBS_ZOEKT_GIT_INDEX": tools["zoekt_git_index"]["path"], "PHEBS_FOCUSED_INDEX": tools["focused_index"]["path"], "PHEBS_BUF": tools["buf"]["path"], "PHEBS_T472_SOURCE_OUTCOME_ROOT": str(root / "source-outcomes"), "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": str(self.rewrite), "GIT_ALLOW_PROTOCOL": "file", "GOMAXPROCS": "2"})
        self.env["PATH"] = str(Path(tools["git"]["path"]).parent) + os.pathsep + str(Path(tools["surreal"]["path"]).parent) + os.pathsep + self.env.get("PATH", "")

    def write_rewrite(self):
        raw = "\n".join(f'[url "file://{r["local_path"]}"]\n\tinsteadOf = https://{r["repository"]}.git\n' for r in self.active_roles.values())
        temporary = None
        try:
            with tempfile.NamedTemporaryFile(mode="w", dir=self.rewrite.parent, prefix=".gitconfig-", delete=False) as output:
                temporary = Path(output.name)
                os.fchmod(output.fileno(), 0o600)
                output.write(raw); output.flush(); os.fsync(output.fileno())
            os.replace(temporary, self.rewrite)
        finally:
            if temporary is not None:
                temporary.unlink(missing_ok=True)

    def check(self):
        self.deadline.remaining()
        if self.monitor:
            self.monitor.check()

    def record_terminal(self, phase, session, terminal):
        phase["terminal"] = {k: terminal[k] for k in ("outcome", "exit_code", "teardown", "wall_seconds")}
        pgid = session.owned_pgid
        score.require(isinstance(pgid, int) and not isinstance(pgid, bool) and pgid > 1, "server ownership lost before terminal group drain")
        phase["owned_pgid"] = pgid
        drain = terminal["teardown"]
        if terminal["teardown"] == "unclean":
            try:
                os.killpg(pgid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            drain = serve.receipts._drain_group(pgid, True)
            phase["forced_group_cleanup"] = drain
        phase["server_group_drained"] = drain in ("clean", "killed_stray_members")
        self.server_groups_drained = phase["server_group_drained"]

    def start(self, name, revoked=None):
        score.require(self.server_groups_drained, "prior server process group remains undrained")
        self.config["release_selection"]["revoked"] = revoked or []
        self.config_path.write_text(json.dumps(self.config)); self.config_path.chmod(0o600)
        self.session = OwnedServeSession(self.root / "harness", name, [self.binding["binary"]["path"], "serve", "-config", str(self.config_path)], self.base, env=self.env, ready_timeout_s=self.deadline.timeout(self.bounds["startup_seconds"]), stop_timeout_s=self.deadline.timeout(15))
        self.server_groups_drained = False
        phase = {"phase": name, "ready_seconds": None}
        self.report["phases"].append(phase)
        # The reused helper has a fixed http_get seam rather than a transport
        # parameter. Scope its readiness GET to this joined total-wall client;
        # startup is serial and no sampling/UI/request thread exists yet.
        original_get = serve.http_get
        def readiness_get(base, path, params=None, token=None, timeout=60):
            remaining = self.bounds["startup_seconds"] - (time.monotonic() - self.session.clock)
            score.require(remaining > 0, "startup HTTP deadline exceeded")
            status, raw, _ = http(base, path, params, token, min(timeout, remaining, self.deadline.remaining()), self.bounds["http_response_bytes"])
            return status, raw
        serve.http_get = readiness_get
        try:
            self.session.start()
        except BaseException:
            path = self.root / "harness" / serve.receipts.RECEIPTS_DIR / (name + ".json")
            if path.exists():
                terminal = checks.bounded_json(path, 65536)
                self.record_terminal(phase, self.session, terminal)
                self.session = None
            raise
        finally:
            serve.http_get = original_get
        phase["ready_seconds"] = self.session.ready_seconds
        score.require(self.session.ready_seconds <= self.bounds["startup_seconds"], "successful startup crossed frozen deadline")
        self.deadline.remaining()
        self.monitor = requalification_monitor.JoinedMonitor(Path(self.binding["observer"]["path"]), self.session.pid, self.root / "data", self.bounds, self.root)
        wait = time.monotonic() + self.deadline.timeout(2)
        while not (self.monitor.samples and self.monitor.headroom and self.monitor.disk):
            self.check(); score.require(time.monotonic() < wait, "initial operating measurement unavailable")
            time.sleep(0.01)

    def stop(self):
        failure = None
        if self.monitor:
            monitor = self.monitor
            try:
                self.report["phases"][-1]["operating"] = monitor.stop()
            except BaseException as error:
                failure = error
                self.report["phases"][-1]["operating_ownership"] = monitor.ownership
            if monitor.joined:
                self.monitor = None
        if self.session is not None and self.session.pid is not None:
            session = self.session
            session.owned_pgid = session.pid
            session.stop_timeout_s = min(15, max(0, self.deadline.limit - self.deadline.clock()))
            terminal = session.stop("graceful")
            self.record_terminal(self.report["phases"][-1], session, terminal)
            self.session = None
            if terminal["outcome"] != "success" or terminal["teardown"] != "clean":
                failure = failure or ValueError("server teardown failed")
        if failure:
            raise failure
        self.deadline.remaining()

    def request(self, path, params=None, maximum=None, authenticated=True, payload=None, extra=None):
        self.check()
        clock = time.monotonic()
        status, raw, kind = http(self.base, path, params, self.token if authenticated else None, self.deadline.timeout(maximum or 5), self.bounds["http_response_bytes"], payload, extra)
        elapsed = time.monotonic() - clock
        if maximum is not None:
            score.require(elapsed <= maximum, "read latency bound exceeded")
        with self.lock:
            self.sequence += 1
            name = f"response-{self.sequence:04}.json"
            (self.raw / name).write_bytes(raw)
            self.report["reads"].append({"path": path, "status": status, "seconds": round(elapsed, 6), "bytes": len(raw), "raw_digest": checks.sha(self.raw / name)})
        self.check()
        return status, raw, kind

    def get(self, path, params=None, maximum=None, authenticated=True):
        status, raw, _ = self.request(path, params, maximum, authenticated)
        return status, raw

    def mcp(self, method, params):
        payload = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}).encode()
        status, raw, kind = self.request("/api/mcp", payload=payload, extra={"Content-Type": "application/json", "Accept": "application/json, text/event-stream"})
        score.require(status == 200, "MCP transport refused")
        return rpc_result(raw, kind)

    def tool(self, name, params):
        return self.mcp("tools/call", {"name": name, "arguments": params})

    def exact(self, params, commits):
        limit = time.monotonic() + self.deadline.timeout(self.bounds["publication_seconds"])
        while True:
            status, raw = self.get("/api/compare_operation_callers", params, self.bounds["cold_request_seconds"])
            page = json.loads(raw)
            score.require(status in (200, 404, 409), "unexpected publication status")
            if status == 200:
                for side in ("old", "replacement"):
                    generation = page.get(side, {}).get("generation", {})
                    score.require(generation.get("state") != "failed" and not generation.get("partition_progress", {}).get("refused_pair_count"), "terminal caller publication failure")
                if page.get("matching_rows_state") == "exact" and all(page[s]["generation"]["commit"] == commits[params[s + "_repository"]] for s in ("old", "replacement")):
                    score.require(time.monotonic() <= limit, "successful publication crossed frozen deadline")
                    self.deadline.remaining()
                    checks.assert_authorities(page, {s: params[s + "_repository"] for s in ("old", "replacement")}, commits)
                    return page, raw
            score.require(time.monotonic() < limit, "publication deadline exceeded")
            time.sleep(min(0.25, self.deadline.remaining()))

    def citation(self, token, truth, repository, commit, file, expected_generation, mcp=False):
        status, raw = self.get("/api/contract_callers/citation", {"citation": token}, self.bounds["cold_request_seconds"])
        score.require(status == 200, "exact source citation refused")
        checks.check_citation(json.loads(raw), truth, repository, commit, file["git_blob"], "sha256:" + file["sha256"], expected_generation)
        if mcp:
            serve.parity(raw, self.tool("read_operation_caller_citation", {"citation": token}), VOLATILE)

    def quality(self, repository):
        commits = {r["repository"]: self.inputs["derived_input_commit"] for r in self.inputs["roles"]}
        files = {f["path"]: f for f in self.inputs["files"]}
        first, _ = self.exact({**qualify.query(self.labels, repository, "occurrence"), "page_size": 100}, commits)
        self.report["initial_authorities"] = {s: first[s]["generation"] for s in ("old", "replacement")}
        self.report["resolver_units"] = [checks.resolver_unit_binding(self.root / "data", repository, commits[repository], first["old"]["generation"], files["unit-snapshot.json"])]
        self.report["caller_authorities"] = [checks.caller_binding(self.root / "data", repository, commits[repository], first["old"]["generation"])]
        output = {}
        self.report["cold_first_exact_response"] = {"occurrence": dict(self.report["reads"][-1])}
        for level in ("occurrence", "unit"):
            params = qualify.query(self.labels, repository, level)
            full_rows = None
            for size in (1, 2, 100):
                pages, cursor = [], ""
                for _ in range(6):
                    current = {**params, "page_size": size}
                    if cursor:
                        current["cursor"] = cursor
                    status, raw = self.get("/api/compare_operation_callers", current, self.bounds["warm_request_seconds"] if pages else self.bounds["cold_request_seconds"])
                    score.require(status == 200, "complete source census refused")
                    page = json.loads(raw); pages.append(page)
                    checks.assert_quality_authorities(page, first, commits)
                    if level == "unit" and size == 1 and len(pages) == 1:
                        self.report["cold_first_exact_response"]["unit"] = dict(self.report["reads"][-1])
                    if page["pagination"]["complete"]:
                        break
                    next_cursor = page["pagination"]["next_cursor"]
                    score.require(next_cursor and next_cursor != cursor, "comparison cursor failed to advance")
                    cursor = next_cursor
                else:
                    raise ValueError("finite source census pagination overflow")
                rows, citations = checks.check_rows(pages, self.labels, self.inputs, level, repository)
                if full_rows is None:
                    full_rows = rows
                    for key, token in citations.items():
                        truth = next(s for s in self.labels["sites"] if s["site_id"] == key)
                        self.citation(token, truth, repository, commits[repository], files[truth["path"]], page[truth["pair_side"]]["generation"], mcp=True)
                if size == 100:
                    serve.parity(raw, self.tool("compare_operation_callers", qualify.mcp_arguments({**params, "page_size": 100})), VOLATILE)
                    for _ in range(5):
                        status, warm = self.get("/api/compare_operation_callers", {**params, "page_size": 100}, self.bounds["warm_request_seconds"])
                        score.require(status == 200, "warm comparison refused")
                        warm_page = json.loads(warm)
                        checks.assert_quality_authorities(warm_page, first, commits)
                        checks.check_rows([warm_page], self.labels, self.inputs, level, repository)
            output[level] = {"rows": len(full_rows), "classes": dict(Counter(r["classification"] for r in full_rows)), "row_metric": score.exact_metric(len(full_rows)), "site_metric": score.exact_metric(5), "resolution_tiers": dict(Counter(item["resolution"] + "/" + item["tier"] for r in full_rows for s in ("old", "replacement") for item in r[s]["rows"]))}
            unit = next(s["unit"]["id"] for s in self.labels["sites"] if s["path"] == "features/multiplex/client/main.go")
            filters = [{"classification": c} for c in ("old_only_evidence", "new_only_evidence", "both_evidence", "unresolved")]
            filters += [{"path_prefix": "features/multiplex/client/"}, {"unit": unit}, {"unit": "au_" + "0" * 64}]
            for selected in filters:
                status, raw = self.get("/api/compare_operation_callers", {**params, **selected, "page_size": 100}, self.bounds["cold_request_seconds"])
                score.require(status == 200, "gold filter refused")
                page = json.loads(raw)
                checks.assert_quality_authorities(page, first, commits)
                score.require(all(page["query"].get(k) == v for k, v in selected.items()), "applied filter echo differs")
                checks.check_rows([page], checks.filtered_truth(self.labels, level, selected), self.inputs, level, repository)
        output["source_processing"] = checks.source_accounting(self.root / "source-outcomes", repository, self.inputs)
        output["outside_pair_exclusion"] = score.exact_metric(4)
        output["unit_attribution"] = score.exact_metric(5)
        output["citation_integrity"] = score.exact_metric(5)
        output["per_file"] = {path: {"selected_sites": len([s for s in self.labels["sites"] if s["path"] == path and s["pair_side"] != "outside_pair"]), "outside_pair": len([s for s in self.labels["sites"] if s["path"] == path and s["pair_side"] == "outside_pair"]), "all_checked": True} for path in sorted({s["path"] for s in self.labels["sites"]})}
        self.report["quality"] = output
        score.require(self.get("/api/compare_operation_callers", qualify.query(self.labels, repository, "occurrence"), authenticated=False)[0] == 401, "anonymous comparison admitted")
        self.capabilities(True, True)

    def capabilities(self, caller, comparison):
        status, raw = self.get("/api/version")
        score.require(status == 200, "version unavailable")
        caps = json.loads(raw)["capabilities"]
        score.require(("contract-caller-map" in caps) == caller and ("contract-caller-comparison" in caps) == comparison, "signed dependency capability differs")
        score.require(not DARK_CAPABILITIES.intersection(caps), "unrelated product activated")
        tools = {t["name"] for t in self.mcp("tools/list", {})["tools"]}
        score.require(("compare_operation_callers" in tools) == comparison and ("list_operation_callers" in tools) == caller, "MCP registration differs from signed dependencies")
        score.require(not DARK_TOOLS.intersection(tools), "unrelated MCP product activated")
        self.report.setdefault("dependency_checks", []).append({"caller_map": caller, "comparison": comparison, "capabilities": caps, "mcp_tools": sorted(tools)})

    def browser(self, repository):
        self.check()
        self.report["browser"] = requalification_browser.run(self.binding, self.root / "browser", {"base": self.base, "token": self.token, "repository": repository, "query": qualify.query(self.labels, repository, "occurrence"), "selectors": [s["selector"] for s in self.labels["sites"] if s["pair_side"] != "outside_pair"]}, lambda: self.deadline.timeout(25))
        self.check()

    def publications(self):
        primary, copy = [r["repository"] for r in self.inputs["roles"]]
        params = qualify.query(self.labels, primary, "unit")
        other = qualify.query(self.labels, copy, "unit")
        params.update({k: v for k, v in other.items() if k.startswith("replacement_")})
        commits = {primary: self.inputs["derived_input_commit"], copy: self.inputs["derived_input_commit"]}
        endpoints = {"old": primary, "replacement": copy}
        page, _ = self.exact({**params, "page_size": 100}, commits); checks.cross_unit_namespace(page)
        self.report["resolver_units"].append(checks.resolver_unit_binding(self.root / "data", copy, commits[copy], page["replacement"]["generation"], next(f for f in self.inputs["files"] if f["path"] == "unit-snapshot.json")))
        self.report["caller_authorities"].append(checks.caller_binding(self.root / "data", copy, commits[copy], page["replacement"]["generation"]))
        params["level"] = "occurrence"
        probes = []
        for transition in self.transitions["records"]:
            before, _ = self.exact({**params, "page_size": 1}, commits)
            cursor = before["pagination"]["next_cursor"]
            score.require(cursor and not before["pagination"]["complete"], "transition probe has no continuation")
            changed_side = "old" if transition["role"] == "primary" else "replacement"
            before_full, _ = self.exact({**params, "page_size": 100}, commits)
            score.require(all(checks.generation_key(before[s]["generation"]) == checks.generation_key(before_full[s]["generation"]) for s in endpoints), "authority changed before controlled transition")
            changed_token = next(i["source"]["citation"] for r in before_full["rows"] for i in r[changed_side]["rows"] if i["source"]["path"] == transition["source_path"])
            self.active_roles[transition["role"]] = transition
            self.write_rewrite()
            payload = json.dumps({"repository": {"clone_url": "https://" + transition["repository"] + ".git"}}).encode()
            signature = "sha256=" + hmac.new(self.webhook_secret.encode(), payload, hashlib.sha256).hexdigest()
            status, _, _ = self.request("/api/webhook", payload=payload, extra={"Content-Type": "application/json", "X-GitHub-Event": "push", "X-Hub-Signature-256": signature})
            score.require(status == 202, "authenticated publication webhook refused")
            with ThreadPoolExecutor(max_workers=8) as pool:
                responses = list(pool.map(lambda _: self.get("/api/compare_operation_callers", {**params, "page_size": 1}, self.bounds["cold_request_seconds"]), range(8)))
            commits[transition["repository"]] = transition["transition_commit"]
            after, _ = self.exact({**params, "page_size": 1}, commits)
            self.report["resolver_units"].append(checks.resolver_unit_binding(self.root / "data", transition["repository"], transition["transition_commit"], after[changed_side]["generation"], next(f for f in self.inputs["files"] if f["path"] == "unit-snapshot.json")))
            self.report["caller_authorities"].append(checks.caller_binding(self.root / "data", transition["repository"], transition["transition_commit"], after[changed_side]["generation"]))
            outcomes = Counter(checks.assert_transition_response(status, json.loads(raw), endpoints, before, after) for status, raw in responses)
            status, _ = self.get("/api/compare_operation_callers", {**params, "page_size": 1, "cursor": cursor})
            score.require(status == 409, "old joint-generation continuation accepted")
            score.require(self.get("/api/contract_callers/citation", {"citation": changed_token})[0] == 409, "changed-side stale citation accepted")
            complete, _ = self.exact({**params, "page_size": 100}, commits)
            item = next(i for r in complete["rows"] for i in r[changed_side]["rows"] if i["source"]["path"] == transition["source_path"])
            truth = next(s for s in self.labels["sites"] if score.site_id(item["source"]) == s["site_id"])
            self.citation(item["source"]["citation"], truth, transition["repository"], transition["transition_commit"], {"git_blob": transition["new_git_blob"], "sha256": transition["new_sha256"]}, complete[changed_side]["generation"])
            probes.append({"role": transition["role"], "new_commit": transition["transition_commit"], "parallel_first_pages": 8, "outcomes": dict(outcomes), "before": {s: before[s]["generation"] for s in endpoints}, "after": {s: after[s]["generation"] for s in endpoints}, "old_cursor_refused": True, "changed_side_old_citation_refused": True})
        self.report["publication_probes"] = probes
        return commits

    def withdrawals(self, commits):
        repository = self.inputs["roles"][0]["repository"]
        query = qualify.query(self.labels, repository, "occurrence")
        self.stop()
        self.start("comparison-withdrawn", ["rel-phebs.grpc.caller.comparison-1.0.0"])
        self.capabilities(True, False)
        score.require(self.get("/api/compare_operation_callers", query)[0] == 404, "revoked comparison HTTP route remains")
        endpoint = {"protocol": "protobuf", "repository": repository, "lineage": query["old_lineage"], "operation": query["old_operation"], "page_size": 100}
        status, raw = self.get("/api/contract_callers", endpoint)
        page = json.loads(raw)
        score.require(status == 200 and page["matching_rows_state"] == "exact" and page["generation"]["commit"] == commits[repository], "comparison withdrawal displaced CallerMap")
        self.stop()
        self.start("caller-dependency-withdrawn", ["rel-phebs.grpc.caller.go-1.0.0"])
        self.capabilities(False, False)
        score.require(self.get("/api/compare_operation_callers", query)[0] == 404 and self.get("/api/contract_callers", endpoint)[0] == 404, "revoked dependency route remains")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binding", type=Path, required=True)
    parser.add_argument("--helpers", type=Path, help="explicit directory containing the reviewed zoekt-git-index and phebs-focused-index bytes")
    parser.add_argument("--run-root", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--execute-reviewed-binding", action="store_true")
    args = parser.parse_args()
    score.require(args.execute_reviewed_binding, "execution requires a reviewed binding and lead native slot")
    plan = checks.check_frozen(HERE)
    binding = checks.bounded_json(args.binding, 262144)
    if args.helpers is not None:
        select_helpers(binding, args.helpers)
    metadata = validate_binding(binding, plan)
    labels = checks.bounded_json(HERE / "labels.json", 65536)
    inputs = checks.bounded_json(HERE / "committed-inputs.json", 65536)
    transitions = checks.bounded_json(HERE / "planned-transitions.json", 65536)
    bounds = plan["measurement_controls"]["bounds"]
    score.require(checks.sha(REPO / "spike/t503/inputs-bundles/base.bundle") == "sha256:" + inputs["bundle"]["sha256"], "frozen base bundle changed")
    input_censuses = []
    for role in inputs["roles"]:
        census = verify_input_repository(binding["tools"]["git"]["path"], role["local_path"], inputs["derived_input_commit"], inputs["derived_input_tree"], inputs["files"])
        input_censuses.append({"role": role["role"], "repository": role["repository"], **census})
    for record in transitions["records"]:
        score.require(checks.sha(HERE / record["bundle"]["path"]) == "sha256:" + record["bundle"]["sha256"], "frozen transition bundle changed")
        files = [dict(f) for f in inputs["files"]]
        changed = next(f for f in files if f["path"] == record["source_path"])
        changed.update(git_blob=record["new_git_blob"], bytes=changed["bytes"] + len(record["appended_utf8"].encode()))
        census = verify_input_repository(binding["tools"]["git"]["path"], record["local_path"], record["transition_commit"], record["transition_tree"], files)
        input_censuses.append({"role": record["role"] + "_prospective_transition", "repository": record["repository"], **census})
    score.require(not args.out.exists() and not args.run_root.exists(), "attempt root/output already exists; no overwrite or retry")
    os.umask(0o077); args.run_root.mkdir(parents=True)
    host = serve.receipts.host_facts()
    score.require(host["sysname"] == "Darwin" and host["machine"] == "arm64", "qualification binding is limited to the measured Darwin arm64 host")
    headroom = qualify.physical_headroom()
    free_disk = shutil.disk_usage(args.run_root).free
    score.require(headroom["available_physical_bytes"] >= bounds["minimum_available_physical_headroom_bytes"] and free_disk >= bounds["minimum_free_disk_bytes"], "physical/disk admission below frozen bound")
    matrix = Matrix(args.run_root, binding, labels, inputs, transitions, bounds)
    matrix.report["input_censuses_before_launch"] = input_censuses
    matrix.report.update(host=host, bounds=bounds, free_disk_preflight_bytes=free_disk, executed_tools=binding["tools"], observer=binding["observer"])
    failure = None
    preparation_started = time.monotonic()
    try:
        artifacts, selection, public, records = selections(args.run_root, binding)
        matrix.config["release_selection"] = {"path": str(selection), "artifacts_path": str(artifacts), "keys": [{"id": "t503-postfix-integration-only", "public_key": public}]}
        matrix.report.update(source_commit=binding["source_commit"], source_tree=binding["source_tree"], binary_digest=checks.sha(Path(binding["binary"]["path"])), binary_metadata=metadata, binding_digest=checks.sha(args.binding), plan_digest=checks.sha(HERE / "requalification-plan.json"), first_attempt_digest=checks.sha(HERE / "baseline-result.json"), labels_digest=checks.sha(HERE / "labels.json"), headroom_preflight=headroom, signed_records=records, qualification="Finite Darwin-local selected public-source composition; no upstream migration/equivalence, deployment/runtime/retirement, global accuracy or Linux scale inference.")
        matrix.deadline = checks.Deadline(bounds["complete_native_seconds"])
        matrix.report["preparation_seconds"] = round(time.monotonic() - preparation_started, 6)
        matrix.start("postfix-baseline")
        matrix.quality(inputs["roles"][0]["repository"])
        matrix.browser(inputs["roles"][0]["repository"])
        commits = matrix.publications()
        matrix.withdrawals(commits)
        matrix.report["outcome"] = "all_frozen_phases_passed_pending_joined_teardown"
    except BaseException as error:
        failure = error
        matrix.report.update(outcome="stopped_no_retry", reason=str(error))
    finally:
        matrix.report.setdefault("preparation_seconds", round(time.monotonic() - preparation_started, 6))
        if matrix.deadline is not None:
            try:
                matrix.stop()
            except BaseException as error:
                failure = failure or error
                matrix.report.update(outcome="stopped_no_retry", cleanup_failure=str(error))
        try:
            matrix.config_path.unlink(missing_ok=True)
            matrix.rewrite.unlink(missing_ok=True)
            score.require(not (args.run_root / "disposable-only.pem").exists(), "disposable private key survived")
            require_derived_cleanup(matrix)
            if (args.run_root / "data").exists():
                shutil.rmtree(args.run_root / "data")
        except BaseException as error:
            failure = failure or error
            matrix.report.update(outcome="stopped_no_retry", custody_cleanup_failure=str(error))
        matrix.report["seconds"] = round(matrix.deadline.elapsed(), 6) if matrix.deadline is not None else 0
        if matrix.report["seconds"] > bounds["complete_native_seconds"]:
            failure = failure or ValueError("global native wall deadline exceeded through cleanup")
            matrix.report.update(outcome="stopped_no_retry", wall_bound_exceeded=True)
        if failure is None:
            matrix.report["outcome"] = "passed_finite_darwin_composition_integration_only_production_signing_pending"
        args.out.write_text(json.dumps(matrix.report, indent=2) + "\n")
        print(json.dumps({"outcome": matrix.report["outcome"], "seconds": matrix.report["seconds"], "receipt": str(args.out)}))
    if failure:
        raise failure


if __name__ == "__main__":
    main()
