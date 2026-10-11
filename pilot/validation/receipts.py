"""Serial run custody and current-host cost receipts for validation rounds.

One run root is held by at most one serial runner; a held lock is never
auto-broken. Each bounded step forks a session leader, joins it, and records
the receipt that a later §12 operating envelope must cite: argv, outcome,
exit/signal, wall seconds, observed peak RSS from ``wait4``, closed host
facts, declared output digests, teardown class, and the bounded retention of
a failed step's workspace. Receipts are written exclusively and never
rewritten, so history cannot be restated after the fact.

Limits kept explicit: child supervision covers the step's own session, so
only same-session descendants are killed and observed; hard-death descendant
supervision beyond the session is T40.13's domain, not this harness's. The
retention walk visits the failed workspace but stores at most the declared
caps, listing every omission in a truncation marker.
"""

from __future__ import annotations

import json
import os
import platform
import re
import shutil
import signal
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

from corpus import canonical_bytes, sha256_file

RUN_LOCK_SCHEMA = "harness-run-lock-v1"
RECEIPT_SCHEMA = "harness-cost-receipt-v1"
TRUNCATION_SCHEMA = "harness-retained-truncation-v1"
RUN_LOCK_NAME = "RUN.lock"
RECEIPTS_DIR = "receipts"
RETAINED_DIR = "retained"
SCRATCH_DIR = "scratch"
RETAINED_FILE_CAP = 4 * 1024 * 1024
RETAINED_TOTAL_CAP = 16 * 1024 * 1024
RETAINED_ENTRY_CAP = 512
POLL_SECONDS = 0.02
GROUP_DRAIN_SECONDS = 2.0
OUTCOMES = ("success", "failed", "stop")
TEARDOWNS = ("clean", "killed_stray_members", "unclean")

_STEP_RE = re.compile(r"\A[A-Za-z0-9][A-Za-z0-9._-]*\Z")
_DIGEST_RE = re.compile(r"\Asha256:[0-9a-f]{64}\Z")
_UTC_RE = re.compile(r"\A\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\Z")


class ReceiptError(ValueError):
    """A run lock, step, workspace, receipt or retention is invalid."""


def utc_now() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _utc(value: object, context: str) -> str:
    if not isinstance(value, str) or not _UTC_RE.match(value):
        raise ReceiptError(f"{context} must be an RFC3339 UTC second")
    return value


def _integer(value: object, context: str, *, minimum: int = 0) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < minimum:
        raise ReceiptError(f"{context} must be an integer >= {minimum}")
    return value


def _step_name(value: object) -> str:
    if not isinstance(value, str) or not _STEP_RE.match(value):
        raise ReceiptError("step must be a clean name of letters, digits, dot, dash or underscore")
    return value


def fsync_dir(path: Path) -> None:
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def ensure_run_layout(run_root: str | os.PathLike[str]) -> Path:
    """Create the receipts/, retained/ and scratch/ skeleton; return the root."""
    root = Path(run_root)
    for name in (RECEIPTS_DIR, RETAINED_DIR, SCRATCH_DIR):
        (root / name).mkdir(parents=True, exist_ok=True)
    return root


def host_facts() -> dict:
    """Closed current-host facts; the receipt host block is never extended ad hoc."""
    try:
        physical = os.sysconf("SC_PHYS_PAGES") * os.sysconf("SC_PAGE_SIZE")
    except (AttributeError, OSError, ValueError) as exc:
        raise ReceiptError(f"physical memory is unavailable on this host: {exc}") from exc
    return {
        "sysname": platform.system(),
        "machine": platform.machine(),
        "cpu_count": os.cpu_count() or 0,
        "physical_memory_bytes": int(physical),
    }


class SerialRunLock:
    """One holder per run root. Acquisition refuses a held lock; release refuses
    a foreign identity, so no runner ever removes another runner's custody."""

    def __init__(self, run_root: str | os.PathLike[str], identity: str):
        if not isinstance(identity, str) or not identity.strip():
            raise ReceiptError("run identity must be a non-empty string")
        self.root = Path(run_root)
        self.identity = identity
        self.path = self.root / RUN_LOCK_NAME

    def held_by(self) -> str | None:
        if not self.path.exists():
            return None
        try:
            value = json.loads(self.path.read_text())
        except (OSError, ValueError) as exc:
            raise ReceiptError(f"run lock is unreadable: {exc}") from exc
        if not isinstance(value, dict) or value.get("schema") != RUN_LOCK_SCHEMA:
            raise ReceiptError("run lock is malformed")
        identity = value.get("identity")
        if not isinstance(identity, str) or not identity:
            raise ReceiptError("run lock names no identity")
        return identity

    def acquire(self) -> None:
        self.root.mkdir(parents=True, exist_ok=True)
        payload = {
            "schema": RUN_LOCK_SCHEMA,
            "identity": self.identity,
            "pid": os.getpid(),
            "acquired_utc": utc_now(),
        }
        try:
            fd = os.open(self.path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        except FileExistsError:
            raise ReceiptError(f"run root is already held by {self.held_by()!r}") from None
        try:
            os.write(fd, canonical_bytes(payload))
            os.fsync(fd)
        finally:
            os.close(fd)
        fsync_dir(self.root)

    def release(self) -> None:
        holder = self.held_by()
        if holder is None:
            raise ReceiptError("run lock is not held")
        if holder != self.identity:
            raise ReceiptError(f"run lock is held by {holder!r}, not {self.identity!r}")
        self.path.unlink()
        fsync_dir(self.root)


def _drain_group(pid: int, timed_out: bool) -> str:
    limit = time.monotonic() + GROUP_DRAIN_SECONDS
    while True:
        try:
            os.killpg(pid, 0)
        except ProcessLookupError:
            return "killed_stray_members" if timed_out else "clean"
        except PermissionError:
            return "unclean"
        if time.monotonic() > limit:
            return "unclean"
        time.sleep(POLL_SECONDS)


def _max_rss_kib(rusage: os.struct_rusage) -> int:
    raw = int(rusage.ru_maxrss)
    # Linux reports kilobytes; the remaining supported platform (Darwin)
    # reports bytes, so record one unit everywhere.
    return raw if sys.platform.startswith("linux") else raw // 1024


def _retain_workspace(workspace: Path, dest: Path, run_root: Path) -> list[str]:
    """Bounded copy of a failed workspace; every omission is named in a marker."""
    dest.mkdir(parents=True, exist_ok=True)
    retained: list[str] = []
    omitted: list[dict] = []
    budget = RETAINED_TOTAL_CAP
    count = 0
    for entry in sorted(workspace.rglob("*")):
        relative = entry.relative_to(workspace)
        if entry.is_symlink():
            omitted.append({"path": str(relative), "reason": "symlink"})
            continue
        if not entry.is_file():
            continue
        count += 1
        size = entry.stat().st_size
        if count > RETAINED_ENTRY_CAP:
            omitted.append({"path": str(relative), "reason": "entry_cap", "bytes": size})
            continue
        if size > RETAINED_FILE_CAP or size > budget:
            omitted.append({"path": str(relative), "reason": "size_cap", "bytes": size})
            continue
        target = dest / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(entry, target)
        budget -= size
        retained.append(str((dest / relative).relative_to(run_root)))
    if omitted:
        marker = dest / "TRUNCATED.json"
        marker.write_bytes(canonical_bytes({"schema": TRUNCATION_SCHEMA, "omitted": omitted}))
        retained.append(str(marker.relative_to(run_root)))
    return sorted(retained)


def run_bounded_step(
    run_root: str | os.PathLike[str],
    step: str,
    argv: list[str],
    timeout_s: float,
    *,
    outputs: tuple[str, ...] = (),
    run_binding_sha256: str | None = None,
    cwd: str | os.PathLike[str] | None = None,
) -> dict:
    """Run one serial bounded step and return its receipt.

    The step gets a fresh workspace under scratch/<step>; it is removed after
    every outcome, and on failure its bounded copy under retained/<step> is
    the custody. A step name is single-use: its receipt exists or it does not.
    """
    step = _step_name(step)
    if isinstance(argv, (str, bytes)) or not isinstance(argv, (list, tuple)) or not argv:
        raise ReceiptError("argv must be a non-empty sequence of strings")
    argv = [arg for arg in argv]
    for arg in argv:
        if not isinstance(arg, str) or not arg:
            raise ReceiptError("argv entries must be non-empty strings")
    if isinstance(timeout_s, bool) or not isinstance(timeout_s, (int, float)) or not timeout_s > 0:
        raise ReceiptError("timeout_s must be a positive number")
    if run_binding_sha256 is not None and (
        not isinstance(run_binding_sha256, str) or not _DIGEST_RE.match(run_binding_sha256)
    ):
        raise ReceiptError("run_binding_sha256 must be a tagged sha256 digest or absent")
    for rel in outputs:
        if not isinstance(rel, str) or not rel or Path(rel).is_absolute() or ".." in Path(rel).parts:
            raise ReceiptError("outputs must be relative paths inside the step workspace")
    root = Path(run_root)
    ensure_run_layout(root)
    receipt_path = root / RECEIPTS_DIR / f"{step}.json"
    if receipt_path.exists():
        raise ReceiptError(f"a receipt for step {step!r} already exists; receipts are written once")
    workspace = root / SCRATCH_DIR / step
    if workspace.exists():
        raise ReceiptError(f"workspace for step {step!r} already exists; refusing to reuse custody")
    workspace.mkdir(parents=True)

    started = utc_now()
    clock = time.monotonic()
    pid = os.fork()
    if pid == 0:  # pragma: no cover - the child either execs or exits
        try:
            os.setsid()
            os.chdir(cwd or workspace)
            os.execvp(argv[0], argv)
        except BaseException:
            os._exit(127)
    timed_out = False
    while True:
        done, status, rusage = os.wait4(pid, os.WNOHANG)
        if done == pid:
            break
        if time.monotonic() - clock > timeout_s:
            timed_out = True
            os.killpg(pid, signal.SIGKILL)
            done, status, rusage = os.wait4(pid, 0)
            break
        time.sleep(POLL_SECONDS)
    wall_seconds = round(time.monotonic() - clock, 3)
    finished = utc_now()

    if os.WIFEXITED(status):
        exit_code: int | None = os.WEXITSTATUS(status)
        signal_number: int | None = None
    elif os.WIFSIGNALED(status):
        exit_code = None
        signal_number = os.WTERMSIG(status)
    else:  # pragma: no cover - wait4 reports only exits and signals
        exit_code = None
        signal_number = None
    if timed_out or signal_number is not None:
        outcome = "stop"
    elif exit_code == 0:
        outcome = "success"
    else:
        outcome = "failed"
    teardown = _drain_group(pid, timed_out)

    declared = []
    for rel in outputs:
        path = workspace / rel
        if path.is_file():
            declared.append({"path": rel, "bytes": path.stat().st_size, "sha256": sha256_file(path)})
        else:
            declared.append({"path": rel, "missing": True})

    retained: list[str] = []
    if outcome != "success":
        retained = _retain_workspace(workspace, root / RETAINED_DIR / step, root)
    shutil.rmtree(workspace)

    receipt = {
        "schema": RECEIPT_SCHEMA,
        "step": step,
        "argv": argv,
        "run_binding_sha256": run_binding_sha256,
        "workspace": f"{SCRATCH_DIR}/{step}",
        "outcome": outcome,
        "exit_code": exit_code,
        "signal": signal_number,
        "timed_out": timed_out,
        "started_utc": started,
        "finished_utc": finished,
        "wall_seconds": wall_seconds,
        "max_rss_kib": _max_rss_kib(rusage),
        "host": host_facts(),
        "teardown": teardown,
        "outputs": declared,
        "retained": retained,
    }
    validate_receipt(receipt)
    fd = os.open(receipt_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        os.write(fd, canonical_bytes(receipt))
        os.fsync(fd)
    finally:
        os.close(fd)
    fsync_dir(receipt_path.parent)
    return receipt


def validate_receipt(value: object) -> dict:
    """Structural and outcome-consistency validation of one cost receipt."""
    fields = {
        "schema", "step", "argv", "run_binding_sha256", "workspace", "outcome", "exit_code", "signal",
        "timed_out", "started_utc", "finished_utc", "wall_seconds", "max_rss_kib", "host", "teardown",
        "outputs", "retained",
    }
    if not isinstance(value, dict) or set(value) != fields:
        raise ReceiptError("receipt: missing or unknown required fields")
    if value["schema"] != RECEIPT_SCHEMA:
        raise ReceiptError(f"receipt is not {RECEIPT_SCHEMA}")
    step = _step_name(value["step"])
    if value["workspace"] != f"{SCRATCH_DIR}/{step}":
        raise ReceiptError("receipt workspace does not match its step")
    argv = value["argv"]
    if not isinstance(argv, list) or not argv or any(not isinstance(arg, str) or not arg for arg in argv):
        raise ReceiptError("receipt argv must be a non-empty list of non-empty strings")
    binding = value["run_binding_sha256"]
    if binding is not None and (not isinstance(binding, str) or not _DIGEST_RE.match(binding)):
        raise ReceiptError("receipt run binding must be a tagged sha256 digest or null")
    outcome = value["outcome"]
    if outcome not in OUTCOMES:
        raise ReceiptError(f"receipt outcome must be one of {OUTCOMES}")
    timed_out = value["timed_out"]
    if not isinstance(timed_out, bool):
        raise ReceiptError("receipt timed_out must be a boolean")
    exit_code = value["exit_code"]
    signal_number = value["signal"]
    if exit_code is not None:
        exit_code = _integer(exit_code, "receipt exit_code")
    if signal_number is not None:
        signal_number = _integer(signal_number, "receipt signal", minimum=1)
    if outcome == "success" and (exit_code != 0 or signal_number is not None or timed_out):
        raise ReceiptError("a successful receipt exits zero without a signal or timeout")
    if outcome == "failed" and (exit_code in (None, 0) or signal_number is not None):
        raise ReceiptError("a failed receipt carries a nonzero exit and no signal")
    if outcome == "stop" and not (timed_out or signal_number is not None):
        raise ReceiptError("a stopped receipt carries a timeout or a signal")
    _utc(value["started_utc"], "receipt started_utc")
    _utc(value["finished_utc"], "receipt finished_utc")
    wall = value["wall_seconds"]
    if isinstance(wall, bool) or not isinstance(wall, (int, float)) or wall < 0:
        raise ReceiptError("receipt wall_seconds must be a nonnegative number")
    _integer(value["max_rss_kib"], "receipt max_rss_kib")
    host = value["host"]
    if not isinstance(host, dict) or set(host) != {"sysname", "machine", "cpu_count", "physical_memory_bytes"}:
        raise ReceiptError("receipt host facts are incomplete or unknown")
    for key in ("sysname", "machine"):
        if not isinstance(host[key], str) or not host[key]:
            raise ReceiptError(f"receipt host.{key} must be a non-empty string")
    _integer(host["cpu_count"], "receipt host.cpu_count", minimum=1)
    _integer(host["physical_memory_bytes"], "receipt host.physical_memory_bytes", minimum=1)
    teardown = value["teardown"]
    if teardown not in TEARDOWNS:
        raise ReceiptError(f"receipt teardown must be one of {TEARDOWNS}")
    if teardown == "clean" and timed_out:
        raise ReceiptError("a timed-out step was not torn down cleanly")
    outputs = value["outputs"]
    if not isinstance(outputs, list):
        raise ReceiptError("receipt outputs must be a list")
    for declared in outputs:
        if not isinstance(declared, dict) or set(declared) not in ({"path", "bytes", "sha256"}, {"path", "missing"}):
            raise ReceiptError("receipt output entries are declared present or missing, nothing else")
        if not isinstance(declared["path"], str) or not declared["path"]:
            raise ReceiptError("receipt output path must be a non-empty string")
        if "bytes" in declared:
            _integer(declared["bytes"], "receipt output bytes")
            if not isinstance(declared["sha256"], str) or not _DIGEST_RE.match(declared["sha256"]):
                raise ReceiptError("receipt output digest must be a tagged sha256 digest")
    retained = value["retained"]
    if not isinstance(retained, list) or any(not isinstance(entry, str) or not entry for entry in retained):
        raise ReceiptError("receipt retained entries must be non-empty strings")
    return dict(value)
