"""Synthetic and isolated-real-Git tests for the P0 executor.

The recording backend deliberately has no filesystem or subprocess behavior.
It verifies the irreversible ordering and the exact M0/R0/T0 handoff without
creating an authorization, derived root, cache, facts, or terminal record.
"""

from __future__ import annotations

import hashlib
import io
import json
import os
import signal
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stderr
from dataclasses import replace
from unittest import mock
from pathlib import Path


HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import stage2_prebuild_execute as executor  # noqa: E402


GIT = Path("/usr/bin/git")


def digest(character: str) -> str:
    return "sha256:" + hashlib.sha256(character.encode("ascii")).hexdigest()


def authorization() -> dict:
    root = Path("/synthetic/private/derived")
    ceremony = Path("/synthetic/private/ceremony")
    source = executor.REPO_ROOT
    facts = root / "spike/t111/stage2-facts"
    heads = {fixture: f"{index:040x}" for index, fixture in enumerate(executor.RECEIPT_FIXTURES, 1)}
    return {
        "authorization_id": "stage2-prebuild-auth-0001",
        "implementation": {
            "prebuild_runner_sha256": digest("a"),
            "prebuild_executor_sha256": digest("b"),
            "enumerator_sha256": digest("c"),
            "enumerator_review_sha256": digest("d"),
        },
        "inputs": {
            "heads": heads,
            "derived_lock_sha256": digest("e"),
            "t111_binary_sha256": digest("f"),
            "typedcalloracle_binary_sha256": digest("1"),
            "estimator_authorization_sha256": digest("4"),
        },
        "prior_authorizations": {
            "estimator": {
                "path": executor.ESTIMATOR_AUTHORIZATION_REL,
                "sha256": digest("4"),
            }
        },
        "toolchain": {
            "git_sha256": digest("2"),
            "go_sha256": digest("3"),
            "producer_toolchain_identity": f'go_version="go";go_digest={digest("3")};git_version="git";git_digest={digest("2")}',
            "git_executable": "/synthetic/bin/git",
            "go_executable": "/synthetic/goroot/bin/go",
        },
        "environment": {
            "hydration": {},
            "offline": {},
        },
        "derived_root": {
            "root": str(root),
            "lock_path": str(root / "spike/t111/corpus.lock.json"),
            "cache_path": str(root / "spike/t111/.module-cache"),
            "corpus_path": str(root / "spike/t111/corpus"),
            "out_path": str(root / "spike/t111/out"),
            "facts_root": str(facts),
        },
        "operations": {
            "source_clone": {"source_repo_path": str(source)},
        },
        "state": {
            "ceremony_directory": str(ceremony),
            "consumption_marker": str(ceremony / "consumed.json"),
            "evidence_receipt": str(ceremony / "evidence.json"),
            "terminal_receipt": str(ceremony / "terminal.json"),
        },
        "bootstrap": {
            "source_t111_path": str(source / "spike/t111/.module-cache/bin/t111"),
            "derived_t111_path": str(root / "spike/t111/.module-cache/bin/t111"),
        },
    }


def context() -> executor.P0Context:
    raw = (executor.canonical_json(authorization()) + "\n").encode("utf-8")
    return executor.context_from_authorization(authorization(), raw)


def sterile_git_environment(root: Path) -> dict[str, str]:
    home = root / "home"
    temporary = root / "tmp"
    home.mkdir(parents=True, exist_ok=True)
    temporary.mkdir(parents=True, exist_ok=True)
    return {
        "PATH": "/usr/bin:/bin",
        "HOME": str(home),
        "TMPDIR": str(temporary),
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": os.devnull,
        "GIT_TERMINAL_PROMPT": "0",
        "GIT_ASKPASS": "/usr/bin/false",
        "GIT_NO_REPLACE_OBJECTS": "1",
        "GIT_NO_LAZY_FETCH": "1",
        "GIT_OPTIONAL_LOCKS": "0",
        "GIT_ALLOW_PROTOCOL": "file",
    }


def run_git(environment: dict[str, str], *arguments: str, cwd: Path | None = None) -> bytes:
    result = subprocess.run(
        [str(GIT), *arguments],
        cwd=str(cwd) if cwd is not None else None,
        env=environment,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if result.returncode:
        raise AssertionError(f"synthetic Git failed: {result.stderr.decode('utf-8', 'replace')}")
    return result.stdout


def synthetic_history(root: Path, fixture: str, environment: dict[str, str]) -> tuple[Path, str, str]:
    repo = root / "sources" / fixture
    repo.parent.mkdir(parents=True, exist_ok=True)
    run_git(environment, "init", "--quiet", str(repo))
    payload = repo / "payload.txt"
    payload.write_text("old\n", encoding="utf-8")
    run_git(environment, "-C", str(repo), "add", "payload.txt")
    run_git(
        environment,
        "-C", str(repo), "-c", "user.name=P0 Test", "-c", "user.email=p0@example.invalid",
        "commit", "--quiet", "-m", "old",
    )
    old = run_git(environment, "-C", str(repo), "rev-parse", "HEAD").decode("ascii").strip()
    payload.write_text("new\n", encoding="utf-8")
    run_git(environment, "-C", str(repo), "add", "payload.txt")
    run_git(
        environment,
        "-C", str(repo), "-c", "user.name=P0 Test", "-c", "user.email=p0@example.invalid",
        "commit", "--quiet", "-m", "new",
    )
    sealed = run_git(environment, "-C", str(repo), "rev-parse", "HEAD").decode("ascii").strip()
    return repo, old, sealed


def controlled_context() -> tuple[
    executor.P0Context, bytes, bytes, bytes, bytes, bytes, bytes, bytes, bytes,
]:
    parser_source = b"synthetic-parser-v1\n"
    executor_source = b"synthetic-executor-v1\n"
    enumerator_source = b"synthetic-enumerator-v1\n"
    module_cache_source = b"synthetic-module-cache-v1\n"
    module_cache_test_source = b"synthetic-module-cache-test-v1\n"
    value = authorization()
    value["implementation"]["prebuild_runner_sha256"] = executor.sha256_bytes(parser_source)
    value["implementation"]["prebuild_executor_sha256"] = executor.sha256_bytes(executor_source)
    value["implementation"]["enumerator_sha256"] = executor.sha256_bytes(enumerator_source)
    enumerator_review = (
        "# enum review\n\n**Verdict: ACCEPT.**\n\n"
        f"{executor._review_binding_line(executor.ENUMERATOR_REL, executor.sha256_bytes(enumerator_source))}\n"
    ).encode("utf-8")
    value["implementation"]["enumerator_review_sha256"] = executor.sha256_bytes(enumerator_review)
    implementation_bindings = executor._implementation_bindings(
        value["implementation"]["prebuild_runner_sha256"],
        value["implementation"]["prebuild_executor_sha256"],
        value["implementation"]["enumerator_sha256"],
        value["implementation"]["enumerator_review_sha256"],
    )
    review_bindings = (
        *implementation_bindings,
        (executor.MODULE_CACHE_REL, executor.sha256_bytes(module_cache_source)),
        (executor.MODULE_CACHE_TEST_REL, executor.sha256_bytes(module_cache_test_source)),
    )
    review = (
        "# review\n\n**Verdict: ACCEPT.**\n\n"
        + "\n".join(
            executor._review_binding_line(relative, artifact_digest)
            for relative, artifact_digest in review_bindings
        )
        + "\n"
    ).encode("utf-8")
    accepted = "a" * 40
    value["implementation_review"] = {
        "status": "accepted",
        "accepted_commit": accepted,
        "record_sha256": executor.sha256_bytes(review),
    }
    value["implementation_binding"] = {"status": "executable", "commit": accepted}
    raw = (executor.canonical_json(value) + "\n").encode("utf-8")
    context_value = executor.context_from_authorization(value, raw)
    executor_bindings = executor._plan_binding_text(
        (*implementation_bindings, (executor.EXECUTOR_REVIEW_REL, executor.sha256_bytes(review)))
    )
    plan = (
        f"| 2026-07-21 | {executor.PLAN_EXECUTOR_MARKER} | {executor.PLAN_EXECUTOR_APPROVAL} | "
        f"{executor_bindings} |\n"
        f"| 2026-07-21 | {executor.PLAN_P0_AUTHORIZATION_MARKER} | "
        f"{executor.PLAN_P0_AUTHORIZATION_APPROVAL} | authorization_id={value['authorization_id']};"
        f"authorization_sha256={context_value.authorization_sha256} |\n"
    ).encode("utf-8")
    return (
        context_value,
        parser_source,
        executor_source,
        review,
        enumerator_source,
        enumerator_review,
        module_cache_source,
        module_cache_test_source,
        plan,
    )


def verify_control(
    context_value: executor.P0Context,
    parser_source: bytes,
    executor_source: bytes,
    review: bytes,
    enumerator_source: bytes,
    enumerator_review: bytes,
    module_cache_source: bytes,
    module_cache_test_source: bytes,
    plan: bytes,
    *,
    head_overrides: dict[str, bytes] | None = None,
    commit_overrides: dict[str, bytes] | None = None,
    ancestor_error: bool = False,
    head_commit_value: str = "b" * 40,
) -> None:
    head = {
        executor.PARSER_REL: parser_source,
        executor.EXECUTOR_REL: executor_source,
        executor.EXECUTOR_REVIEW_REL: review,
        executor.ENUMERATOR_REL: enumerator_source,
        executor.ENUMERATOR_REVIEW_REL: enumerator_review,
        executor.MODULE_CACHE_REL: module_cache_source,
        executor.MODULE_CACHE_TEST_REL: module_cache_test_source,
        executor.PLAN_REL: plan,
        executor.P0_AUTHORIZATION_REL: context_value.authorization_bytes,
    }
    if head_overrides:
        head.update(head_overrides)
    accepted = context_value.authorization["implementation_review"]["accepted_commit"]
    committed = {
        executor.PARSER_REL: parser_source,
        executor.EXECUTOR_REL: executor_source,
        executor.EXECUTOR_REVIEW_REL: review,
        executor.ENUMERATOR_REL: enumerator_source,
        executor.ENUMERATOR_REVIEW_REL: enumerator_review,
        executor.MODULE_CACHE_REL: module_cache_source,
        executor.MODULE_CACHE_TEST_REL: module_cache_test_source,
        executor.PLAN_REL: plan,
    }
    if commit_overrides:
        committed.update(commit_overrides)

    def head_blob(relative: str) -> bytes:
        return head[relative]

    def commit_blob(commit: str, relative: str) -> bytes:
        if commit != accepted:
            raise AssertionError("unexpected synthetic commit")
        return committed[relative]

    def require_ancestor(_commit: str, _label: str) -> None:
        if ancestor_error:
            raise executor.PrebuildExecutionError("not an ancestor")

    executor.verify_implementation_chain(
        context_value,
        parser_source=parser_source,
        executor_source=executor_source,
        review_bytes=review,
        enumerator_source=enumerator_source,
        enumerator_review_bytes=enumerator_review,
        module_cache_source=module_cache_source,
        module_cache_test_source=module_cache_test_source,
        plan_bytes=plan,
        head_blob=head_blob,
        commit_blob=commit_blob,
        require_ancestor=require_ancestor,
        head_commit=lambda: head_commit_value,
    )


class RecordingBackend:
    def __init__(
        self,
        *,
        fail_at: str | None = None,
        unexpected: bool = False,
        marker_indeterminate: bool = False,
        different_facts: bool = False,
        signal_at: str | None = None,
    ):
        self.fail_at = fail_at
        self.unexpected = unexpected
        self.marker_indeterminate = marker_indeterminate
        self.different_facts = different_facts
        self.signal_at = signal_at
        self.events: list[str] = []
        self.evidence: dict | None = None
        self.terminals: list[dict] = []

    def _step(self, name: str) -> None:
        self.events.append(name)
        if name == self.signal_at:
            os.kill(os.getpid(), signal.SIGTERM)
        if name == self.fail_at:
            if self.unexpected:
                raise RuntimeError("synthetic crash")
            raise executor.PrebuildExecutionError("synthetic refusal")

    def consume(self, _context, transition):
        transition["started"] = True
        transition["marker_sha256"] = digest("m")
        self._step("M0")
        if self.marker_indeterminate:
            raise executor.MarkerTransitionError("synthetic marker failure", digest("m"))
        return digest("m")

    def clone_source(self, _context):
        self._step("clone")

    def rewrite_lock(self, _context):
        self._step("lock")

    def materialize_corpus(self, _context):
        self._step("corpus")

    def prepare_fresh_cache(self, _context):
        self._step("cache")

    def hydrate(self, _context):
        self._step("hydrate")
        return {
            fixture: {
                "exit_code": 0,
                "stdout_sha256": digest("h"),
                "stderr_sha256": digest("i"),
            }
            for fixture in executor.RECEIPT_FIXTURES
        }

    def finalize_fixture_configs(self, _context):
        self._step("finalize")

    def bind_and_seal_cache(self, _context):
        self._step("seal-cache")
        return executor.DerivedResult(digest("e"), digest("j"), digest("k"))

    def extract_facts(self, value, _derived):
        self._step("extract")
        facts = {fixture: digest("q") for fixture in executor.RECEIPT_FIXTURES}
        second = dict(facts)
        if self.different_facts:
            second["loki"] = digest("r")
        return {
            "run1": executor.FactRunResult("prebuild-run1-0001", str(value.root / "spike/t111/stage2-facts/run1"), digest("s"), facts, {"stdout_sha256": digest("t"), "stderr_sha256": digest("u")}),
            "run2": executor.FactRunResult("prebuild-run2-0001", str(value.root / "spike/t111/stage2-facts/run2"), digest("v"), second, {"stdout_sha256": digest("t"), "stderr_sha256": digest("u")}),
        }

    def seal_execution_root(self, _context):
        self._step("seal-root")

    def publish_evidence(self, _context, value):
        self._step("R0")
        self.evidence = value
        return digest("w")

    def publish_terminal(self, _context, value):
        self._step("T0")
        self.terminals.append(value)
        return digest("x")


class ExecutorSyntheticTests(unittest.TestCase):
    def test_git_quiet_uses_signal_handoff_safe_process_creation(self):
        process = mock.Mock()
        process.returncode = 0
        with (
            mock.patch.object(
                executor, "_popen_without_signal_handoff_gap", return_value=process
            ) as spawn,
            mock.patch.object(
                executor, "_communicate_with_group_timeout", return_value=(b"ok\n", b"")
            ),
        ):
            self.assertEqual(
                executor._git_quiet(
                    Path("/synthetic/bin/git"),
                    ["/synthetic/bin/git", "status"],
                    cwd=Path("/synthetic"),
                    env={"PATH": "/synthetic/bin"},
                ),
                b"ok\n",
            )
        spawn.assert_called_once()

    def test_offline_phase_scratch_is_initialized_before_first_corpus_command(self):
        value = context()
        value.authorization["operations"]["corpus"] = {
            "fixtures": {
                fixture: {
                    "source_ref": f"refs/gate2-v2/{fixture}",
                    "source_commit": f"{index:040x}",
                    "source_repo_path": "/synthetic/source",
                    "update_ref_argv": ["synthetic-ref"],
                    "bundle_argv": ["synthetic-bundle"],
                }
                for index, fixture in enumerate(executor.RECEIPT_FIXTURES, 1)
            }
        }
        backend = executor.LiveBackend()
        with (
            mock.patch.object(backend, "_phase_environment", return_value={}) as phase,
            mock.patch.object(executor, "_mkdir_private_below"),
            mock.patch.object(executor, "_git_quiet", return_value=b""),
            mock.patch.object(
                backend, "_run_quiet", side_effect=executor.PrebuildExecutionError("refusal")
            ),
        ):
            with self.assertRaises(executor.PrebuildExecutionError):
                backend.materialize_corpus(value)
        phase.assert_called_once_with(value, "offline")

    def test_phase_environment_reuses_its_owned_scratch_not_stale_state(self):
        value = context()
        scratch = value.ceremony / "scratch" / "offline"
        value.authorization["environment"]["offline"] = {
            "scratch_root": str(scratch),
            "variables": {"PATH": "/synthetic/bin"},
            "proxy": "off",
            "sumdb": "off",
        }
        backend = executor.LiveBackend()
        created: list[Path] = []
        with (
            mock.patch.object(executor, "_mkdir_private_below", side_effect=lambda path, _anchor: created.append(path)),
            mock.patch.object(executor, "_mkdir_private", side_effect=lambda path: created.append(path)),
            mock.patch.object(executor, "_safe_local_git_environment", return_value={"PATH": "/synthetic/bin"}),
        ):
            self.assertEqual(backend._phase_environment(value, "offline"), {"PATH": "/synthetic/bin"})
            self.assertEqual(backend._phase_environment(value, "offline"), {"PATH": "/synthetic/bin"})
        self.assertEqual(
            created,
            [
                scratch,
                scratch / "tmp",
                scratch / "home",
                scratch / "xdg-config",
                scratch / "xdg-cache",
                scratch / "xdg-data",
            ],
        )

    @unittest.skipUnless(GIT.is_file(), "requires the bound system Git")
    def test_real_git_executes_ref_bundle_init_fetch_and_checkout(self):
        """Exercise every ref-bearing corpus argv class without network access."""
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            environment = sterile_git_environment(root)
            control = root / "control"
            control.mkdir()
            derived = root / "derived"
            ceremony = root / "ceremony"
            derived.mkdir()
            ceremony.mkdir(mode=0o700)
            fixtures: dict[str, dict[str, object]] = {}
            source_heads: dict[str, tuple[Path, str, str]] = {}
            for fixture in executor.RECEIPT_FIXTURES:
                source, old, sealed = synthetic_history(root, fixture, environment)
                source_heads[fixture] = (source, old, sealed)
                source_ref = f"refs/gate2-v2/{fixture}"
                bundle = ceremony / "bundles" / f"{fixture}.bundle"
                destination = derived / "spike" / "t111" / "corpus" / fixture
                fixtures[fixture] = {
                    "source_repo_path": str(source),
                    "source_commit": sealed,
                    "source_ref": source_ref,
                    "destination_path": str(destination),
                    "bundle_path": str(bundle),
                    "update_ref_argv": [
                        str(GIT), "-C", str(source), "update-ref", source_ref, sealed, "0" * 40,
                    ],
                    "bundle_argv": [
                        str(GIT), "-C", str(source), "bundle", "create", str(bundle), source_ref,
                    ],
                    "init_argv": [str(GIT), "init", "--quiet", str(destination)],
                    "fetch_argv": [
                        str(GIT), "-C", str(destination), "fetch", "--no-tags", str(bundle),
                        f"{source_ref}:{source_ref}",
                    ],
                    "checkout_argv": [str(GIT), "-C", str(destination), "checkout", "--detach", sealed],
                }
                raw_bundle = root / "raw" / f"{fixture}.bundle"
                raw_bundle.parent.mkdir(exist_ok=True)
                raw = subprocess.run(
                    [str(GIT), "-C", str(source), "bundle", "create", str(raw_bundle), sealed],
                    env=environment,
                    stdin=subprocess.DEVNULL,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    check=False,
                )
                self.assertNotEqual(raw.returncode, 0, fixture)

            value = context()
            value.authorization["operations"]["corpus"] = {
                "fixtures": fixtures,
            }
            context_value = replace(
                value,
                root=derived,
                source_repo=control,
                ceremony=ceremony,
                git=GIT,
            )
            backend = executor.LiveBackend()
            with mock.patch.object(backend, "_phase_environment", return_value=environment):
                backend.materialize_corpus(context_value)
            for fixture, (source, _old, sealed) in source_heads.items():
                source_ref = f"refs/gate2-v2/{fixture}"
                self.assertEqual(
                    run_git(environment, "-C", str(source), "rev-parse", source_ref).decode("ascii").strip(),
                    sealed,
                )
                destination = Path(fixtures[fixture]["destination_path"])
                self.assertEqual(
                    run_git(environment, "-C", str(destination), "rev-parse", "HEAD").decode("ascii").strip(),
                    sealed,
                )

    @unittest.skipUnless(GIT.is_file(), "requires the bound system Git")
    def test_real_git_shallow_mirror_refuses_before_m0_or_ref_creation(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            environment = sterile_git_environment(root)
            control, _control_old, control_head = synthetic_history(root, "control", environment)
            fixtures: dict[str, dict[str, object]] = {}
            mirrors = root / "mirrors"
            for fixture in executor.RECEIPT_FIXTURES:
                source, old, sealed = synthetic_history(root, fixture, environment)
                mirror = mirrors / fixture
                mirror.parent.mkdir(parents=True, exist_ok=True)
                run_git(environment, "clone", "--quiet", "--depth=1", f"file://{source}", str(mirror))
                source_ref = f"refs/gate2-v2/{fixture}"
                fixtures[fixture] = {
                    "source_repo_path": str(mirror),
                    "source_commit": sealed,
                    "source_ref": source_ref,
                    "source_history": {
                        "is_shallow_repository": False,
                        "old_commit": old,
                        "sealed_commit": sealed,
                        "old_commit_is_ancestor": True,
                    },
                }
                self.assertEqual(
                    run_git(environment, "-C", str(mirror), "rev-parse", "--is-shallow-repository"),
                    b"true\n",
                )

            value = context()
            value.authorization["operations"]["source_clone"] = {"source_commit": control_head}
            value.authorization["operations"]["corpus"] = {"fixtures": fixtures}
            value.authorization["implementation_binding"] = {
                "status": "executable", "commit": control_head,
            }
            value.authorization["environment"]["offline"] = {
                "variables": environment,
                "proxy": "off",
                "sumdb": "off",
            }
            derived = root / "derived"
            ceremony = root / "ceremony"
            context_value = replace(
                value,
                root=derived,
                source_repo=control,
                ceremony=ceremony,
                git=GIT,
            )
            with self.assertRaises(executor.PrebuildExecutionError):
                executor._verify_source_repositories(context_value)
            self.assertFalse(derived.exists())
            self.assertFalse(ceremony.exists())
            for fixture in executor.RECEIPT_FIXTURES:
                mirror = Path(fixtures[fixture]["source_repo_path"])
                self.assertEqual(
                    run_git(
                        environment,
                        "-C", str(mirror), "for-each-ref", "--format=%(objectname)",
                        f"refs/gate2-v2/{fixture}",
                    ),
                    b"",
                )

    @unittest.skipUnless(GIT.is_file(), "requires the bound system Git")
    def test_post_m0_git_verification_failure_has_a_bounded_diagnostic(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            environment = sterile_git_environment(root)
            repository, _old, _sealed = synthetic_history(root, "verify", environment)
            with self.assertRaises(executor.CommandFailure) as raised:
                executor._git_quiet(
                    GIT,
                    [str(GIT), "-C", str(repository), "rev-parse", "missing-ref"],
                    cwd=repository,
                    env=environment,
                    diagnostic_step="corpus.temporal.checkout.head",
                )
            diagnostic = raised.exception.diagnostic
            self.assertEqual(diagnostic.step, "corpus.temporal.checkout.head")
            self.assertNotEqual(diagnostic.exit_code, 0)
            self.assertTrue(diagnostic.stderr_sha256.startswith("sha256:"))

    @unittest.skipUnless(Path("/bin/sh").is_file(), "requires the system shell")
    def test_post_m0_git_diagnostic_drains_and_bounds_stdout(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            with self.assertRaises(executor.PrebuildExecutionError):
                executor._git_quiet(
                    Path("/bin/sh"),
                    ["/bin/sh", "-c", "dd if=/dev/zero bs=100000 count=1 2>/dev/null"],
                    cwd=root,
                    env=sterile_git_environment(root),
                    diagnostic_step="corpus.temporal.ref.verify",
                )

    def test_command_failure_terminal_diagnostic_is_bounded_and_sanitized(self):
        raw = (
            b"fatal: https://alice:ghp_secret@example.invalid/x?access_token=query-secret\r\n"
            b"Authorization: Bearer bearer-secret\r\n"
            b"Authorization=Bearer assignment-secret\r\n"
            b"access_token=access-secret client_secret: client-secret X-Api-Key: api-secret\r\n"
            b"{\"access_token\":\"json-secret\"}\r\n"
            + b"x" * 1024
        )
        capture = executor._BoundedStderr(limit=512)
        capture.add(raw)
        diagnostic = capture.diagnostic("corpus.temporal.bundle", 128)
        self.assertEqual(diagnostic.stderr_sha256, executor.sha256_bytes(raw))
        self.assertTrue(diagnostic.stderr_truncated)
        self.assertNotIn("alice", diagnostic.stderr_preview)
        self.assertNotIn("ghp_secret", diagnostic.stderr_preview)
        for secret in (
            "query-secret",
            "bearer-secret",
            "assignment-secret",
            "access-secret",
            "client-secret",
            "api-secret",
            "json-secret",
        ):
            self.assertNotIn(secret, diagnostic.stderr_preview)
        terminal = executor._terminal_record(
            context(),
            digest("m"),
            status="ABORTED",
            exit_code=2,
            failure="REFUSED",
            failure_diagnostic=diagnostic,
            evidence_sha256=None,
        )
        self.assertEqual(terminal["schema"], executor.P0_TERMINAL_RECEIPT_SCHEMA)
        self.assertEqual(terminal["failure_diagnostic"], diagnostic.record())
        completed = executor._terminal_record(
            context(),
            digest("m"),
            status="COMPLETED",
            exit_code=0,
            failure=None,
            failure_diagnostic=None,
            evidence_sha256=digest("e"),
        )
        self.assertIsNone(completed["failure_diagnostic"])

    def test_diagnostic_redacts_url_userinfo_cut_off_by_the_bound(self):
        raw = b"fatal https://alice:LEAKMARKER" + b"x" * 1024 + b"@example.invalid\n"
        capture = executor._BoundedStderr(limit=64)
        capture.add(raw)
        preview = capture.diagnostic("corpus.temporal.bundle", 2).stderr_preview
        self.assertNotIn("alice", preview)
        self.assertNotIn("LEAKMARKER", preview)
        self.assertIn("[REDACTED]", preview)

    def test_timeout_keeps_a_sanitized_failing_step_diagnostic(self):
        class TimedOutProcess:
            returncode = -signal.SIGTERM

        raw = b"Authorization: Bearer timeout-secret\n"

        def expire(
            process: TimedOutProcess, _timeout: float, capture: executor._BoundedStderr, **_kwargs,
        ) -> None:
            capture.add(raw)
            raise subprocess.TimeoutExpired("synthetic", 1)

        with self.subTest("quiet"):
            with mock.patch.object(
                executor, "_popen_without_signal_handoff_gap", return_value=TimedOutProcess(),
            ), mock.patch.object(executor, "_wait_with_bounded_stderr", side_effect=expire):
                with self.assertRaises(executor.CommandFailure) as raised:
                    executor.LiveBackend()._run_quiet(
                        ["synthetic"],
                        cwd=Path("/synthetic"),
                        environment={},
                        step="source.clone",
                    )
            diagnostic = raised.exception.diagnostic
            self.assertEqual(diagnostic.step, "source.clone")
            self.assertEqual(diagnostic.exit_code, -signal.SIGTERM)
            self.assertEqual(diagnostic.stderr_sha256, executor.sha256_bytes(raw))
            self.assertNotIn("timeout-secret", diagnostic.stderr_preview)

        with self.subTest("capture-many"):
            with tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary).resolve()
                with mock.patch.object(
                    executor, "_popen_without_signal_handoff_gap", return_value=TimedOutProcess(),
                ), mock.patch.object(executor, "_wait_with_bounded_stderr", side_effect=expire):
                    with self.assertRaises(executor.CommandFailure) as raised:
                        executor.LiveBackend()._run_capture_many(
                            [["synthetic"]],
                            steps=["extract.run1.temporal"],
                            cwd=root,
                            environment={},
                            stdout_path=root / "capture" / "stdout",
                            stderr_path=root / "capture" / "stderr",
                            private_anchor=root,
                        )
                diagnostic = raised.exception.diagnostic
                self.assertEqual(diagnostic.step, "extract.run1.temporal")
                self.assertEqual(diagnostic.exit_code, -signal.SIGTERM)
                self.assertEqual(diagnostic.stderr_sha256, executor.sha256_bytes(raw))
                self.assertNotIn("timeout-secret", diagnostic.stderr_preview)

    def test_capture_many_diagnostic_is_only_the_failing_command_stderr(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            stdout_path = root / "capture" / "stdout"
            stderr_path = root / "capture" / "stderr"
            with self.assertRaises(executor.CommandFailure) as raised:
                executor.LiveBackend()._run_capture_many(
                    [
                        ["/bin/sh", "-c", "printf 'token=prior-noise\\n' >&2"],
                        ["/bin/sh", "-c", "printf 'fatal token=second-secret\\n' >&2; exit 9"],
                    ],
                    steps=["extract.run1.temporal", "extract.run1.loki"],
                    cwd=root,
                    environment=sterile_git_environment(root),
                    stdout_path=stdout_path,
                    stderr_path=stderr_path,
                    private_anchor=root,
                )
            diagnostic = raised.exception.diagnostic
            self.assertEqual(diagnostic.step, "extract.run1.loki")
            self.assertEqual(diagnostic.exit_code, 9)
            self.assertEqual(
                diagnostic.stderr_sha256,
                executor.sha256_bytes(b"fatal token=second-secret\n"),
            )
            self.assertNotIn("prior-noise", diagnostic.stderr_preview)
            self.assertNotIn("second-secret", diagnostic.stderr_preview)
            self.assertEqual(diagnostic.stderr_preview, "fatal token=[REDACTED]")
            self.assertEqual(
                stderr_path.read_bytes(),
                b"token=prior-noise\nfatal token=second-secret\n",
            )

    def test_control_chain_accepts_only_exact_committed_review_executor_and_both_plan_anchors(self):
        verify_control(*controlled_context())

    def test_control_chain_refuses_each_review_commit_or_plan_link_before_m0(self):
        (
            context_value,
            parser_source,
            executor_source,
            review,
            enumerator_source,
            enumerator_review,
            module_cache_source,
            module_cache_test_source,
            plan,
        ) = controlled_context()
        cases = (
            ("review digest", {"review": b"wrong"}),
            ("executor commit blob", {"commit_overrides": {executor.EXECUTOR_REL: b"wrong"}}),
            ("parser commit blob", {"commit_overrides": {executor.PARSER_REL: b"wrong"}}),
            ("review commit blob", {"commit_overrides": {executor.EXECUTOR_REVIEW_REL: b"wrong"}}),
            ("enumerator commit blob", {"commit_overrides": {executor.ENUMERATOR_REL: b"wrong"}}),
            ("enumerator review commit blob", {"commit_overrides": {executor.ENUMERATOR_REVIEW_REL: b"wrong"}}),
            ("module cache commit blob", {"commit_overrides": {executor.MODULE_CACHE_REL: b"wrong"}}),
            ("module cache test commit blob", {"commit_overrides": {executor.MODULE_CACHE_TEST_REL: b"wrong"}}),
            ("implementation plan anchor", {"plan": plan.splitlines(keepends=True)[1]}),
            (
                "accepted implementation plan omission",
                {"commit_overrides": {executor.PLAN_REL: b"| unrelated acceptance |\n"}},
            ),
            (
                "accepted implementation plan exact-row mismatch",
                {"commit_overrides": {executor.PLAN_REL: plan.replace(b" |\n", b"  |\n", 1)}},
            ),
            (
                "accepted implementation plan digest mismatch",
                {
                    "commit_overrides": {
                        executor.PLAN_REL: plan.replace(
                            executor.sha256_bytes(executor_source).encode("ascii"),
                            digest("z").encode("ascii"),
                            1,
                        )
                    }
                },
            ),
            ("p0 committed bytes", {"head_overrides": {executor.P0_AUTHORIZATION_REL: b"wrong"}}),
            ("p0 plan approval", {"plan": plan.splitlines(keepends=True)[0]}),
            ("review ancestor", {"ancestor_error": True}),
            (
                "implementation review is the P0 authorization HEAD",
                {"head_commit_value": context_value.authorization["implementation_review"]["accepted_commit"]},
            ),
        )
        for name, changes in cases:
            with self.subTest(name=name):
                kwargs = dict(changes)
                changed_review = kwargs.pop("review", review)
                changed_plan = kwargs.pop("plan", plan)
                with self.assertRaises(executor.PrebuildExecutionError):
                    verify_control(
                        context_value,
                        parser_source,
                        executor_source,
                        changed_review,
                        enumerator_source,
                        enumerator_review,
                        module_cache_source,
                        module_cache_test_source,
                        changed_plan,
                        **kwargs,
                    )

    def test_review_and_plan_acceptance_use_only_the_exact_grammar(self):
        (
            context_value,
            parser_source,
            executor_source,
            review,
            enumerator_source,
            enumerator_review,
            module_cache_source,
            module_cache_test_source,
            plan,
        ) = controlled_context()
        implementation = context_value.authorization["implementation"]
        bindings = executor._implementation_bindings(
            implementation["prebuild_runner_sha256"],
            implementation["prebuild_executor_sha256"],
            implementation["enumerator_sha256"],
            implementation["enumerator_review_sha256"],
        )
        review_bindings = (
            *bindings,
            (executor.MODULE_CACHE_REL, executor.sha256_bytes(module_cache_source)),
            (executor.MODULE_CACHE_TEST_REL, executor.sha256_bytes(module_cache_test_source)),
        )
        for bad_verdict in (
            b"**Verdict: ACCEPTED.**",
            b"**Verdict: NOT ACCEPT.**",
            b'"**Verdict: ACCEPT.**"',
        ):
            with self.subTest(bad_verdict=bad_verdict):
                with self.assertRaises(executor.PrebuildExecutionError):
                    executor._require_accepted_review(
                        review.replace(executor.REVIEW_ACCEPT_VERDICT.encode("utf-8"), bad_verdict),
                        label="synthetic review",
                        bindings=review_bindings,
                    )
        with self.assertRaises(executor.PrebuildExecutionError):
            executor._require_accepted_review(
                review + b"\n**Verdict: REJECT.**\n",
                label="synthetic review",
                bindings=review_bindings,
            )
        review_without_parser = review.replace(
            executor._review_binding_line(review_bindings[0][0], review_bindings[0][1]).encode("utf-8"),
            b"- `quoted/path`: `sha256:" + b"0" * 64 + b"`",
        )
        with self.assertRaises(executor.PrebuildExecutionError):
            executor._require_accepted_review(
                review_without_parser, label="synthetic review", bindings=review_bindings
            )
        with self.assertRaises(executor.PrebuildExecutionError):
            executor._require_accepted_review(
                review.replace(b"\n", b"\r\n"),
                label="synthetic review",
                bindings=review_bindings,
            )
        review_digest = context_value.authorization["implementation_review"]["record_sha256"]
        for bad_approval in ("ACCEPTED", "NOT ACCEPT", '"ACCEPT"'):
            with self.subTest(bad_approval=bad_approval):
                malformed = plan.replace(
                    f"| {executor.PLAN_EXECUTOR_APPROVAL} |".encode("utf-8"),
                    f"| {bad_approval} |".encode("utf-8"),
                    1,
                )
                with self.assertRaises(executor.PrebuildExecutionError):
                    executor._executor_plan_anchor(
                        malformed,
                        review_digest=review_digest,
                        implementation_bindings=bindings,
                        label="synthetic PLAN",
                    )
        with self.assertRaises(executor.PrebuildExecutionError):
            executor._executor_plan_anchor(
                plan.replace(b" | ", b"  | ", 1),
                review_digest=review_digest,
                implementation_bindings=bindings,
                label="synthetic PLAN",
            )
        with self.assertRaises(executor.PrebuildExecutionError):
            executor._executor_plan_anchor(
                plan.replace(b"\n", b"\r\n"),
                review_digest=review_digest,
                implementation_bindings=bindings,
                label="synthetic PLAN",
            )

    def test_context_refuses_a_noncanonical_source_repository(self):
        value = authorization()
        value["operations"]["source_clone"]["source_repo_path"] = "/synthetic/external"
        raw = (executor.canonical_json(value) + "\n").encode("utf-8")
        with self.assertRaises(executor.PrebuildExecutionError):
            executor.context_from_authorization(value, raw)

    def test_base_lock_must_match_the_sealed_stage0_inventory_row(self):
        base_lock = b"[\n  {\"name\": \"fixture\"}\n]\n"
        base_digest = executor.sha256_bytes(base_lock)
        inventory = (
            b"# synthetic inventory\n"
            + b"spike/t111/corpus.lock.json\t"
            + base_digest.encode("ascii")
            + b"\n"
        )
        inputs = {
            "stage0_inventory_sha256": executor.sha256_bytes(inventory),
            "base_lock_sha256": base_digest,
        }
        executor._verify_stage0_inventory_lock_binding(inventory, base_lock, inputs)
        conflicting_inventory = inventory.replace(
            base_digest.encode("ascii"), digest("z").encode("ascii")
        )
        inputs["stage0_inventory_sha256"] = executor.sha256_bytes(conflicting_inventory)
        with self.assertRaises(executor.PrebuildExecutionError):
            executor._verify_stage0_inventory_lock_binding(
                conflicting_inventory, base_lock, inputs
            )

    def test_ceremony_parent_is_fsynced_before_the_marker_oexcl_edge(self):
        calls: list[str] = []
        transition: dict[str, object] = {}

        def mkdir(path: Path) -> None:
            self.assertEqual(path, context().ceremony)
            calls.append("mkdir")

        def fsync(path: Path, label: str) -> None:
            self.assertEqual(path, context().ceremony.parent)
            self.assertEqual(label, "ceremony parent")
            calls.append("fsync-parent")

        def marker_write(path: Path, _data: bytes, label: str, *, transition: dict) -> str:
            self.assertEqual(path, context().marker)
            self.assertEqual(label, "consumption marker")
            calls.append("marker-oexcl")
            transition.update({"started": True, "marker_sha256": digest("m")})
            return digest("m")

        with (
            mock.patch.object(executor, "_mkdir_private", side_effect=mkdir),
            mock.patch.object(executor, "_fsync_directory", side_effect=fsync),
            mock.patch.object(executor, "_write_exclusive_durable", side_effect=marker_write),
        ):
            self.assertEqual(executor.LiveBackend().consume(context(), transition), digest("m"))
        self.assertEqual(calls, ["mkdir", "fsync-parent", "marker-oexcl"])

        with (
            mock.patch.object(executor, "_mkdir_private"),
            mock.patch.object(
                executor,
                "_fsync_directory",
                side_effect=executor.PrebuildExecutionError("synthetic fsync failure"),
            ),
            mock.patch.object(executor, "_write_exclusive_durable") as marker_write,
        ):
            with self.assertRaises(executor.PrebuildExecutionError):
                executor.LiveBackend().consume(context(), {})
        marker_write.assert_not_called()

    def test_authoritative_control_checks_are_repeated_with_the_digest_verified_git(self):
        context_value = context()
        sealed_git = Path("/synthetic/bin/sealed-git")
        with (
            mock.patch.object(
                executor,
                "_load_parser_admission",
                return_value=(context_value.authorization, context_value.authorization_bytes, b"parser"),
            ),
            mock.patch.object(executor, "_verify_toolchain", return_value=sealed_git),
            mock.patch.object(executor, "_head_commit", side_effect=["b" * 40, "b" * 40]),
            mock.patch.object(executor, "_require_clean_control_worktree") as clean,
            mock.patch.object(executor, "_verify_committed_implementation") as implementation,
            mock.patch.object(executor, "_verify_sealed_inputs"),
            mock.patch.object(executor, "_verify_prior_ceremony_registry"),
            mock.patch.object(executor, "_verify_fresh_namespaces"),
        ):
            self.assertEqual(executor.admit_live(), context_value)
        clean.assert_called_once_with(control_git=sealed_git)
        implementation.assert_called_once_with(
            context_value, b"parser", control_git=sealed_git
        )

        with (
            mock.patch.object(
                executor,
                "_load_parser_admission",
                return_value=(context_value.authorization, context_value.authorization_bytes, b"parser"),
            ),
            mock.patch.object(executor, "_verify_toolchain", return_value=sealed_git),
            mock.patch.object(executor, "_head_commit", side_effect=["b" * 40, "c" * 40]),
            mock.patch.object(executor, "_require_clean_control_worktree") as clean,
        ):
            with self.assertRaises(executor.PrebuildExecutionError):
                executor.admit_live()
        clean.assert_not_called()

    def test_bootstrap_refuses_altered_parser_before_execution(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            p0_path = root / "p0.json"
            parser_path = root / "parser.py"
            expected_parser = b"# expected parser bytes\n"
            parser_path.write_text("raise AssertionError('altered parser executed')\n")
            p0 = {
                "implementation": {
                    "prebuild_runner_sha256": executor.sha256_bytes(expected_parser)
                }
            }
            raw = (executor.canonical_json(p0) + "\n").encode("utf-8")
            p0_path.write_bytes(raw)

            def head_blob(relative: str) -> bytes:
                if relative == "synthetic/p0.json":
                    return raw
                if relative == "synthetic/parser.py":
                    return parser_path.read_bytes()
                raise AssertionError(f"unexpected control blob: {relative}")

            with (
                mock.patch.object(executor, "P0_AUTHORIZATION_PATH", p0_path),
                mock.patch.object(executor, "P0_AUTHORIZATION_REL", "synthetic/p0.json"),
                mock.patch.object(executor, "PARSER_PATH", parser_path),
                mock.patch.object(executor, "PARSER_REL", "synthetic/parser.py"),
                mock.patch.object(executor, "_head_blob", side_effect=head_blob),
                mock.patch.object(executor, "_execute_verified_source") as execute,
            ):
                with self.assertRaises(executor.PrebuildExecutionError):
                    executor._load_parser_admission()
            execute.assert_not_called()

    def test_fact_copy_and_parser_reject_hardlinks_and_failure_records(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            source = root / "source.jsonl"
            destination = root / "destination.jsonl"
            source.write_text(json.dumps({"predicate": "LOAD_ERRORS"}) + "\n")
            executor._copy_regular_no_hardlink(source, destination)
            with self.assertRaises(executor.PrebuildExecutionError):
                executor._validate_fact_jsonl(destination)

            linked = root / "linked.jsonl"
            os.link(source, linked)
            with self.assertRaises(executor.PrebuildExecutionError):
                executor._copy_regular_no_hardlink(source, root / "blocked.jsonl")

    def test_cache_sealing_refuses_hardlinked_regular_entries_before_digesting(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            cache = root / "cache"
            cache.mkdir()
            first = cache / "first"
            second = cache / "second"
            first.write_bytes(b"sealed cache entry\n")
            os.link(first, second)
            with self.assertRaises(executor.PrebuildExecutionError):
                executor._make_tree_readonly(
                    cache, reject_symlinks=True, reject_hardlinks=True
                )
            with self.assertRaises(executor.PrebuildExecutionError):
                executor._cache_tree_digest(cache)

    def test_inert_git_config_rejects_hooks_and_symlinks_when_sealing(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            git_dir = root / "repo" / ".git"
            (git_dir / "objects" / "info").mkdir(parents=True)
            (git_dir / "config").write_bytes(executor.INERT_GIT_CONFIG)
            executor._verify_inert_git_repository(root / "repo", root / "source", "synthetic repo")
            (git_dir / "hooks").mkdir()
            with self.assertRaises(executor.PrebuildExecutionError):
                executor._verify_inert_git_repository(root / "repo", root / "source", "synthetic repo")
            (git_dir / "hooks").rmdir()
            link = root / "repo" / "linked"
            link.symlink_to(git_dir / "config")
            with self.assertRaises(executor.PrebuildExecutionError):
                executor._make_tree_readonly(root / "repo", reject_symlinks=True)

    def test_prior_ceremony_reservation_is_committed_and_blocks_overlap(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            prior_ceremony = root / "ceremony" / "prior"
            prior = {
                "schema": "t111-estimator-authorization-v1",
                "authorization_id": "prior-estimator-auth",
                "state": {
                    "admission_receipt": str(prior_ceremony / "admission.json"),
                    "consumption_marker": str(prior_ceremony / "consumed.json"),
                    "result_receipt": str(prior_ceremony / "result.json"),
                },
                "inputs": {
                    "label_publication_receipt": str(
                        root / "ceremony" / "attempt-3" / "label-receipt.json"
                    )
                },
            }
            prior_raw = (executor.canonical_json(prior) + "\n").encode("utf-8")
            prior_path = root / "estimator-authorization.json"
            prior_path.write_bytes(prior_raw)
            value = authorization()
            new_ceremony = root / "ceremony" / "p0"
            value["state"] = {
                "ceremony_directory": str(new_ceremony),
                "consumption_marker": str(new_ceremony / "consumed.json"),
                "evidence_receipt": str(new_ceremony / "evidence.json"),
                "terminal_receipt": str(new_ceremony / "terminal.json"),
            }
            value["inputs"]["estimator_authorization_sha256"] = executor.sha256_bytes(prior_raw)
            value["prior_authorizations"]["estimator"] = {
                "path": "synthetic/estimator.json",
                "sha256": executor.sha256_bytes(prior_raw),
            }
            raw = (executor.canonical_json(value) + "\n").encode("utf-8")
            context_value = executor.context_from_authorization(value, raw)
            with (
                mock.patch.object(executor, "ESTIMATOR_AUTHORIZATION_PATH", prior_path),
                mock.patch.object(executor, "ESTIMATOR_AUTHORIZATION_REL", "synthetic/estimator.json"),
                mock.patch.object(executor, "_head_blob", return_value=prior_raw),
            ):
                executor._verify_prior_ceremony_registry(context_value)
                value["state"]["ceremony_directory"] = str(prior_ceremony)
                value["state"]["consumption_marker"] = str(prior_ceremony / "new-consumed.json")
                value["state"]["evidence_receipt"] = str(prior_ceremony / "new-evidence.json")
                value["state"]["terminal_receipt"] = str(prior_ceremony / "new-terminal.json")
                overlapping_raw = (executor.canonical_json(value) + "\n").encode("utf-8")
                with self.assertRaises(executor.PrebuildExecutionError):
                    executor._verify_prior_ceremony_registry(
                        executor.context_from_authorization(value, overlapping_raw)
                    )
                attempt_three = root / "ceremony" / "attempt-3"
                value["state"] = {
                    "ceremony_directory": str(attempt_three),
                    "consumption_marker": str(attempt_three / "new-consumed.json"),
                    "evidence_receipt": str(attempt_three / "new-evidence.json"),
                    "terminal_receipt": str(attempt_three / "new-terminal.json"),
                }
                attempt_three_raw = (executor.canonical_json(value) + "\n").encode("utf-8")
                with self.assertRaises(executor.PrebuildExecutionError):
                    executor._verify_prior_ceremony_registry(
                        executor.context_from_authorization(value, attempt_three_raw)
                    )

    def test_happy_path_orders_m0_before_every_mutation_and_publishes_complete_chain(self):
        backend = RecordingBackend()
        outcome = executor.execute_context(context(), backend)
        self.assertEqual(outcome.exit_code, 0)
        self.assertEqual(
            backend.events,
            ["M0", "clone", "lock", "corpus", "cache", "hydrate", "finalize", "seal-cache", "extract", "seal-root", "R0", "T0"],
        )
        self.assertIsNotNone(backend.evidence)
        self.assertEqual(set(backend.evidence), {
            "schema", "status", "authorization_id", "authorization_sha256", "consumption_marker_sha256",
            "implementation", "inputs", "toolchain", "environment", "derived", "hydration", "fact_runs",
        })
        self.assertEqual(backend.evidence["fact_runs"]["run1"]["run_id"], "prebuild-run1-0001")
        self.assertEqual(backend.terminals[-1]["status"], "COMPLETED")
        self.assertIsNone(backend.terminals[-1]["failure"])
        self.assertIsNone(backend.terminals[-1]["failure_diagnostic"])
        self.assertEqual(backend.terminals[-1]["evidence_receipt_sha256"], digest("w"))

    def test_refusal_after_m0_publishes_only_aborted_terminal(self):
        backend = RecordingBackend(fail_at="corpus")
        outcome = executor.execute_context(context(), backend)
        self.assertEqual(outcome.exit_code, 2)
        self.assertEqual(backend.events, ["M0", "clone", "lock", "corpus", "T0"])
        self.assertIsNone(backend.evidence)
        self.assertEqual(backend.terminals[-1]["status"], "ABORTED")
        self.assertEqual(backend.terminals[-1]["failure"], "REFUSED")
        self.assertIsNone(backend.terminals[-1]["failure_diagnostic"])
        self.assertIsNone(backend.terminals[-1]["evidence_receipt_sha256"])

    def test_abort_handoff_masks_a_second_signal_until_terminal_is_durable(self):
        backend = RecordingBackend(fail_at="clone")
        original = executor._terminal_record

        def record_with_second_signal(*args, **kwargs):
            os.kill(os.getpid(), signal.SIGTERM)
            return original(*args, **kwargs)

        with mock.patch.object(executor, "_terminal_record", side_effect=record_with_second_signal):
            outcome = executor.execute_context(context(), backend)
        self.assertEqual(outcome.exit_code, 2)
        self.assertEqual(outcome.status, "ABORTED")
        self.assertEqual(backend.events, ["M0", "clone", "T0"])
        self.assertEqual(backend.terminals[-1]["status"], "ABORTED")
        self.assertEqual(backend.terminals[-1]["failure"], "REFUSED")

    def test_abort_retries_if_a_signal_interrupts_its_first_masking_instruction(self):
        backend = RecordingBackend(fail_at="clone")
        original = executor._block_catchable_signals
        calls = 0

        def interrupt_first_arm():
            nonlocal calls
            calls += 1
            if calls == 1:
                raise executor.P0Interrupted(signal.SIGTERM)
            return original()

        with mock.patch.object(executor, "_block_catchable_signals", side_effect=interrupt_first_arm):
            outcome = executor.execute_context(context(), backend)
        self.assertEqual(outcome.exit_code, 4)
        self.assertEqual(outcome.status, "ABORTED")
        self.assertEqual(backend.events, ["M0", "clone", "T0"])
        self.assertEqual(backend.terminals[-1]["failure"], "INTERRUPTED")

    def test_post_m0_terminalizer_call_boundary_cannot_escape_without_t0(self):
        backend = RecordingBackend(fail_at="clone")
        original = executor._abort_until_terminal
        calls = 0

        def interrupt_first_terminalizer_call(*args, **kwargs):
            nonlocal calls
            calls += 1
            if calls == 1:
                raise executor.P0Interrupted(signal.SIGTERM)
            return original(*args, **kwargs)

        with mock.patch.object(
            executor,
            "_abort_until_terminal",
            side_effect=interrupt_first_terminalizer_call,
        ):
            outcome = executor.execute_context(context(), backend)
        self.assertEqual(outcome.exit_code, 4)
        self.assertEqual(outcome.status, "ABORTED")
        self.assertEqual(backend.events, ["M0", "clone", "T0"])
        self.assertEqual(backend.terminals[-1]["failure"], "INTERRUPTED")

    def test_latched_signal_before_r0_cutover_publishes_aborted_not_completed(self):
        backend = RecordingBackend()
        original = executor._block_catchable_signals
        calls = 0

        def signal_at_final_chain_arm():
            nonlocal calls
            calls += 1
            if calls == 1:
                os.kill(os.getpid(), signal.SIGTERM)
            return original()

        with mock.patch.object(
            executor, "_block_catchable_signals", side_effect=signal_at_final_chain_arm
        ):
            outcome = executor.execute_context(context(), backend)
        self.assertEqual(outcome.exit_code, 4)
        self.assertEqual(outcome.status, "ABORTED")
        self.assertNotIn("R0", backend.events)
        self.assertEqual(backend.events[-1], "T0")
        self.assertEqual(backend.terminals[-1]["failure"], "INTERRUPTED")

    def test_unexpected_crash_has_distinct_exit_four_and_aborted_terminal(self):
        backend = RecordingBackend(fail_at="cache", unexpected=True)
        outcome = executor.execute_context(context(), backend)
        self.assertEqual(outcome.exit_code, 4)
        self.assertEqual(backend.events, ["M0", "clone", "lock", "corpus", "cache", "T0"])
        self.assertEqual(backend.terminals[-1]["failure"], "UNEXPECTED")

    def test_catchable_signals_after_the_oexcl_edge_publish_one_aborted_terminal(self):
        for signal_at, expected_events in (
            ("M0", ["M0", "T0"]),
            ("cache", ["M0", "clone", "lock", "corpus", "cache", "T0"]),
        ):
            with self.subTest(signal_at=signal_at):
                backend = RecordingBackend(signal_at=signal_at)
                outcome = executor.execute_context(context(), backend)
                self.assertEqual(outcome.exit_code, 4)
                self.assertEqual(outcome.status, "ABORTED")
                self.assertEqual(backend.events, expected_events)
                self.assertEqual(backend.terminals[-1]["failure"], "INTERRUPTED")

    def test_signal_during_terminal_publication_cannot_replace_completed_terminal(self):
        backend = RecordingBackend(signal_at="T0")
        outcome = executor.execute_context(context(), backend)
        self.assertEqual(outcome.exit_code, 0)
        self.assertEqual(outcome.status, "COMPLETED")
        self.assertEqual(backend.events[-1], "T0")
        self.assertEqual(backend.terminals[-1]["status"], "COMPLETED")

    def test_nonreproducible_facts_fail_before_evidence_publication(self):
        backend = RecordingBackend(different_facts=True)
        outcome = executor.execute_context(context(), backend)
        self.assertEqual(outcome.exit_code, 2)
        self.assertEqual(backend.events, ["M0", "clone", "lock", "corpus", "cache", "hydrate", "finalize", "seal-cache", "extract", "T0"])
        self.assertIsNone(backend.evidence)
        self.assertEqual(backend.terminals[-1]["status"], "ABORTED")

    def test_indeterminate_marker_never_starts_derived_work(self):
        backend = RecordingBackend(marker_indeterminate=True)
        outcome = executor.execute_context(context(), backend)
        self.assertEqual(outcome.exit_code, 4)
        self.assertEqual(backend.events, ["M0", "T0"])
        self.assertEqual(backend.terminals[-1]["failure"], "MARKER_PERSISTENCE")

    def test_admission_refusal_never_creates_marker(self):
        backend = RecordingBackend()

        def refuse():
            raise executor.PrebuildExecutionError("synthetic admission refusal")

        outcome = executor.run_with_admission(refuse, backend)
        self.assertEqual(outcome.exit_code, 2)
        self.assertEqual(backend.events, [])

    def test_main_maps_terminal_persistence_and_unexpected_failures_to_one_exit_four_line(self):
        for failure in (
            executor.TerminalPersistenceError("synthetic terminal failure"),
            RuntimeError("synthetic crash"),
        ):
            with self.subTest(failure=type(failure).__name__):
                stderr = io.StringIO()
                with (
                    mock.patch.object(sys, "argv", ["stage2_prebuild_execute.py", "--execute"]),
                    mock.patch.object(executor, "run_with_admission", side_effect=failure),
                    redirect_stderr(stderr),
                ):
                    self.assertEqual(executor.main(), 4)
                self.assertEqual(
                    stderr.getvalue(), "stage2-prebuild-execute: execution failed\n"
                )


if __name__ == "__main__":
    unittest.main()
