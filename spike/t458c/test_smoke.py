"""Pure malformed-envelope/custody controls; no server, Git, source or scorer run."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import time
import unittest
from unittest import mock

import smoke


def fixture():
    population = [
        {"name": "t307", "repository": "local/neutral/t307", "commit": "a" * 40, "catalog_id": "t335-demo", "catalog_version": "v1", "required_services": ["orders-api", "orders-events"]},
        {"name": "t323", "repository": "local/neutral/t323", "commit": "b" * 40, "catalog_id": "t344-demo", "catalog_version": "v1", "required_services": ["svc.orders-api", "svc.fulfillment"]},
    ]
    root = {"state": "complete", "repository": population[0]["repository"], "service_key": "orders-api", "root_schema": "relationship-root-v2", "generation": "sha256:" + "1" * 64, "root_digest": "sha256:" + "2" * 64, "authority_digest": "sha256:" + "3" * 64}
    value = {"repositories": [], "relationships": {"schema": "phebs-service-relationship-page-v1", "query": {"repositories": [population[0]["repository"]], "service_key": "orders-api", "view": "all"}, "rows_state": "nonempty", "roots": [root], "rows": [{"repository": population[0]["repository"], "service_key": "orders-api"}], "coverage": {"authorized_repositories": 1, "complete_roots": 1, "failed_roots": 0, "unavailable_roots": 0, "truncated": False}}}
    for selected in population:
        value["repositories"].append({"name": selected["repository"], "indexed_commit_hash": selected["commit"], "last_index_job_state": "exact", "last_index_job": {"status": "done"}, "last_extraction_job_state": "exact", "last_extraction_job": {"status": "done"}})
        value[selected["name"] + "Services"] = {"schema": "phebs-service-inventory-v1", "repository": {"repository": selected["repository"], "source_commit": selected["commit"], "authority": {"kind": "operator", "id": selected["catalog_id"], "version": selected["catalog_version"]}, "catalog_service_count": 5}, "services": [{"key": key, "disposition": "accepted", "status": "current", "removed": False} for key in selected["required_services"]]}
    return value, population


class SmokeControls(unittest.TestCase):
    def test_exact_cohort_and_pending_states(self):
        initial, population = fixture()
        self.assertTrue(smoke.readiness(initial, population))
        for mutate in (
            lambda value: value["repositories"].pop(),
            lambda value: value["repositories"][0]["last_index_job"].update(status="running"),
            lambda value: value["t307Services"]["services"][0].update(status="unavailable"),
            lambda value: value["relationships"]["coverage"].update(complete_roots=0, unavailable_roots=1),
            lambda value: value["relationships"].update(rows_state="empty", rows=[]),
        ):
            value = copy.deepcopy(initial); mutate(value)
            self.assertFalse(smoke.readiness(value, population))

    def test_malformed_crossed_and_failed_states_refuse(self):
        initial, population = fixture()
        for mutate in (
            lambda value: value["repositories"][0].update(name="foreign"),
            lambda value: value["repositories"].append(copy.deepcopy(value["repositories"][0])),
            lambda value: value["repositories"][0].update(indexed_commit_hash="c" * 40),
            lambda value: value["repositories"][0]["last_extraction_job"].update(status="failed"),
            lambda value: value["repositories"][0].update(indexed_analysis_unit={"name": "orders-service"}),
            lambda value: value["t307Services"]["repository"].update(repository="foreign"),
            lambda value: value["t307Services"]["repository"].update(source_commit="c" * 40),
            lambda value: value["t323Services"]["repository"].update(source_commit="c" * 40),
            lambda value: value["t307Services"]["repository"]["authority"].update(id="foreign"),
            lambda value: value["t323Services"]["repository"]["authority"].update(id="foreign"),
            lambda value: value["t307Services"]["repository"]["authority"].update(version="v2"),
            lambda value: value["t323Services"]["repository"]["authority"].update(version="v2"),
            lambda value: value["t323Services"]["repository"]["authority"].update(kind="manifest"),
            lambda value: value["relationships"]["query"].update(repositories=["foreign"]),
            lambda value: value["relationships"]["roots"][0].update(generation=""),
            lambda value: value["relationships"]["roots"][0].update(repository="foreign"),
        ):
            value = copy.deepcopy(initial); mutate(value)
            with self.assertRaises(ValueError):
                smoke.readiness(value, population)

    def test_service_authority_cannot_fall_back_or_cross_source(self):
        _, population = fixture(); selected = population[0]
        authority = {"repository": selected["repository"], "service_key": "orders-api", "revision_commit": selected["commit"]}
        authority.update({key: "sha256:" + "1" * 64 for key in ("repository_source_generation", "repository_search_generation", "active_source_generation", "digest")})
        initial = {"files": [{"path": "service/orders/service.go", "repo": selected["repository"], "ref": selected["commit"]}], "scope_receipt": {"schema": "phebs-search-scope-v1", "kind": "service", "repository": selected["repository"], "service_key": "orders-api", "service_status": "current", "service_authority": authority, "revisions": [{"repository": selected["repository"], "commit": selected["commit"]}]}}
        smoke.check_search(initial, selected)
        for mutate in (
            lambda value: value["scope_receipt"].update(kind="all_code"),
            lambda value: value["scope_receipt"]["service_authority"].pop("repository_source_generation"),
            lambda value: value["files"][0].update(ref="c" * 40),
            lambda value: value["scope_receipt"]["service_authority"].update(service_key="other"),
        ):
            value = copy.deepcopy(initial); mutate(value)
            with self.assertRaises(ValueError):
                smoke.check_search(value, selected)

    def test_citation_checks_actual_blob_span_and_page_generation(self):
        path = "service/orders/service.go"
        blob = (smoke.REPO / "docs/fixtures/t30.7-neutral-service/repo" / path).read_bytes()
        start, end = 0, len(b"package orders")
        evidence = {"path": path, "object_id": hashlib.sha1(b"blob " + str(len(blob)).encode() + b"\0" + blob).hexdigest(), "content_digest": "sha256:" + hashlib.sha256(blob).hexdigest(), "span": {"start_byte": start, "end_byte": end}}
        page, population = fixture(); page = page["relationships"]
        row = {"evidence": evidence, "projection_digest": "sha256:" + "4" * 64}
        initial = dict(page["roots"][0], schema="phebs-service-relationship-citation-v1", evidence=evidence, projection={"digest": row["projection_digest"]}, content=blob[start:end].decode())
        smoke.check_citation(initial, page, row, population[0])
        for mutate in (
            lambda value: value.update(generation="sha256:" + "5" * 64),
            lambda value: value.update(repository="foreign"),
            lambda value: value["evidence"].update(object_id="f" * 40),
            lambda value: value.update(content="wrong selector"),
        ):
            value = copy.deepcopy(initial); mutate(value)
            with self.assertRaises(ValueError):
                smoke.check_citation(value, page, row, population[0])

    def test_failed_cleanup_retains_only_raw_and_diagnostic_after_join(self):
        with tempfile.TemporaryDirectory() as parent:
            root = Path(parent) / "run"; root.mkdir()
            value = smoke.Smoke.__new__(smoke.Smoke)
            value.root, value.raw, value.config_path = root, root / "private-raw", root / "config.json"
            value.raw.mkdir(); (value.raw / "response-0001.json").write_text("{}")
            value.config_path.write_text("secret-not-retained")
            (root / "data").mkdir(); (root / "data" / "state").write_text("derived")
            value.server_groups_drained, value.monitor = False, None
            value.report = {"outcome": "stopped"}
            value.deadline = smoke.checks.Deadline(5)
            with self.assertRaises(ValueError):
                value.cleanup(True)
            self.assertTrue(value.config_path.exists())
            value.server_groups_drained = True
            value.cleanup(True)
            self.assertEqual({path.name for path in root.iterdir()}, {"private-raw", "stop-diagnostic.json"})
            self.assertEqual(json.loads((root / "stop-diagnostic.json").read_text())["outcome"], "stopped")

    def test_recorder_refuses_before_an_unretainable_request(self):
        value = smoke.Smoke.__new__(smoke.Smoke)
        value.plan = {"bounds": {"http_calls": 10, "response_bytes": 20, "private_raw_bytes": 100}}
        value.report, value.raw_bytes = {"reads": []}, 81
        with mock.patch.object(smoke.shared, "http") as http:
            with self.assertRaises(ValueError):
                value.get("/api/repo-status")
            http.assert_not_called()

    def test_total_http_cap_covers_health_get_and_failed_attempts(self):
        for health_fails in (False, True):
            with self.subTest(health_fails=health_fails), tempfile.TemporaryDirectory() as parent:
                value = smoke.Smoke.__new__(smoke.Smoke)
                value.plan = {"bounds": {"http_calls": 2, "response_bytes": 20, "private_raw_bytes": 100, "request_seconds": 5, "startup_seconds": 30, "cleanup_reserve_seconds": 15}}
                value.report = {"http_calls": 0, "health_reads": [], "reads": []}
                value.raw, value.raw_bytes = Path(parent), 0
                value.base, value.token = "http://127.0.0.1:1234", "not-a-runtime-key"
                value.deadline = smoke.checks.Deadline(180)
                value.session = mock.Mock(clock=time.monotonic())
                first = OSError("unavailable") if health_fails else (200, b"{}", "application/json")
                with mock.patch.object(smoke.shared, "http", side_effect=[first, (200, b"{}", "application/json")]) as http:
                    if health_fails:
                        with self.assertRaises(OSError):
                            value.readiness_health(value.base, "/api/health")
                    else:
                        self.assertEqual(value.readiness_health(value.base, "/api/health"), (200, b"{}"))
                    self.assertEqual(value.get("/api/repo-status"), (200, {}))
                    for request in (lambda: value.get("/api/repo-status"), lambda: value.readiness_health(value.base, "/api/health")):
                        with self.assertRaisesRegex(ValueError, "HTTP call bound exceeded"):
                            request()
                    self.assertEqual(http.call_count, 2)
                self.assertEqual(value.report["http_calls"], 2)
                self.assertEqual(len(value.report["health_reads"]), 1)
                self.assertEqual(len(value.report["reads"]), 1)
                self.assertNotIn("content", value.report["health_reads"][0])
                if health_fails:
                    self.assertEqual(value.report["health_reads"][0]["error_type"], "OSError")
                self.assertEqual({path.name for path in value.raw.iterdir()}, {"response-0001.json"})

    def test_stop_reserves_helper_tail_and_rejects_forced_cleanup(self):
        for left, expected_grace in ((15, 8), (9, 2), (5, 0)):
            value = smoke.Smoke.__new__(smoke.Smoke)
            value.deadline = smoke.checks.Deadline(left, clock=lambda: 0)
            value.report = {"phases": [{}]}
            value.session = mock.Mock(pid=123, owned_pgid=None)
            session = value.session
            session.stop.return_value = {"outcome": "success", "teardown": "clean"}
            value.record_terminal = mock.Mock()
            value.stop()
            self.assertEqual(session.stop_timeout_s, expected_grace)
            self.assertEqual(session.owned_pgid, 123)
            session.stop.assert_called_once_with("graceful")
            self.assertIsNone(value.session)
        for terminal in ({"outcome": "stop", "teardown": "clean"}, {"outcome": "success", "teardown": "killed_stray_members"}):
            value.session = mock.Mock(pid=123)
            value.session.stop.return_value = terminal
            with self.assertRaises(ValueError):
                value.stop()
            self.assertIsNone(value.session)

    def test_expired_clock_still_joins_before_refusing(self):
        clock = mock.Mock(return_value=0)
        value = smoke.Smoke.__new__(smoke.Smoke)
        value.deadline = smoke.checks.Deadline(1, clock=clock)
        clock.return_value = 2
        value.report = {"phases": [{}]}
        value.session = mock.Mock(pid=123)
        session = value.session
        session.stop.return_value = {"outcome": "success", "teardown": "clean"}
        value.record_terminal = mock.Mock()
        with self.assertRaises(ValueError):
            value.stop()
        session.stop.assert_called_once_with("graceful")
        self.assertEqual(session.stop_timeout_s, 0)
        self.assertIsNone(value.session)


if __name__ == "__main__":
    unittest.main()
