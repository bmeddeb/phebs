#!/usr/bin/env python3
"""GATE2-V2 Stage 1 — the one authorized snapshot request.

Authorized by the Stage-0 acceptance decision (PLAN.md, 2026-07-16). Fires
exactly one non-paginated GraphQL request at the sealed cutoff, validates
it fail-closed, and seals the response and receipt as Stage-1 artifacts.

Fail-closed properties:
  - the sealed query and constants are digest-verified against STAGE0.md's
    artifact manifest before anything else;
  - the attempt is exclusive: stage1/ is created with mkdir (no reuse); a
    second invocation refuses. A failed attempt seals a rejection receipt
    and the round stops — re-firing requires a newly reviewed cutoff;
  - the request must start within max_clock_skew_seconds of the cutoff,
    and the response's Date header must agree with the local UTC clock
    within the same constant;
  - any transport failure, non-200, GraphQL error, missing fixture field,
    or malformed head oid rejects the round. No retry.

Usage:
  stage1_snapshot.py --wait   sleep until the sealed cutoff, then fire
  stage1_snapshot.py --now    fire immediately (window still enforced)
GITHUB_TOKEN must be present in the environment at fire time.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import sys
import time
import urllib.request
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from pathlib import Path

STAGE0 = Path(__file__).resolve().parent / "stage0"
STAGE1 = Path(__file__).resolve().parent / "stage1"
ENDPOINT = "https://api.github.com/graphql"
RECEIPT_SCHEMA = "t111-gate2-v2-stage1-receipt-v1"
OID_RE = re.compile(r"^[0-9a-f]{40}$|^[0-9a-f]{64}$")


class SnapshotError(Exception):
    pass


def sha256_bytes(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


def manifest_digests() -> dict[str, str]:
    text = (STAGE0 / "STAGE0.md").read_text(encoding="utf-8")
    section = text.split("## 8. Complete artifact manifest")[1]
    return dict(re.findall(r"\| `([^`]+)` \| `(sha256:[0-9a-f]{64})` \|", section))


def load_sealed_inputs() -> tuple[bytes, dict]:
    digests = manifest_digests()
    query_bytes = (STAGE0 / "snapshot-query.graphql").read_bytes()
    constants_bytes = (STAGE0 / "snapshot-constants.json").read_bytes()
    for name, data in (("snapshot-query.graphql", query_bytes),
                       ("snapshot-constants.json", constants_bytes)):
        if digests.get(name) != sha256_bytes(data):
            raise SnapshotError(f"sealed input {name} does not match the Stage-0 manifest")
    return query_bytes, json.loads(constants_bytes)


def default_transport(query_bytes: bytes, token: str) -> tuple[int, dict[str, str], bytes]:
    body = json.dumps({"query": query_bytes.decode("utf-8")}).encode("utf-8")
    request = urllib.request.Request(
        ENDPOINT, data=body, method="POST",
        headers={"Authorization": f"bearer {token}",
                 "Content-Type": "application/json",
                 "User-Agent": "phebs-t111-gate2-v2-stage1"})
    with urllib.request.urlopen(request, timeout=60) as response:
        return response.status, dict(response.headers), response.read()


def validate_response(status: int, headers: dict[str, str], body: bytes,
                      constants: dict, now: datetime) -> dict:
    if status != 200:
        raise SnapshotError(f"HTTP status {status}")
    server_date = None
    for key, value in headers.items():
        if key.lower() == "date":
            try:
                server_date = parsedate_to_datetime(value)
            except (ValueError, TypeError) as exc:
                raise SnapshotError(f"malformed Date header: {value!r}") from exc
    if server_date is None:
        raise SnapshotError("response has no Date header")
    skew = abs((now - server_date.astimezone(timezone.utc)).total_seconds())
    if skew > constants["max_clock_skew_seconds"]:
        raise SnapshotError(f"clock skew {skew:.1f}s exceeds sealed maximum")
    try:
        document = json.loads(body)
    except json.JSONDecodeError as exc:
        raise SnapshotError(f"response body is not JSON: {exc}") from exc
    if not isinstance(document, dict):
        raise SnapshotError("response body is not a JSON object")
    if document.get("errors"):
        raise SnapshotError(f"GraphQL errors: {json.dumps(document['errors'])[:400]}")
    data = document.get("data") or {}
    aliases = {"temporal": "temporal", "dapr": "dapr", "loki": "loki",
               "online-boutique": "onlineBoutique"}
    heads = {}
    for fixture, alias in aliases.items():
        repo = data.get(alias)
        if not repo:
            raise SnapshotError(f"fixture {fixture} missing from response")
        ref = (repo.get("defaultBranchRef") or {})
        target = (ref.get("target") or {})
        oid, committed = target.get("oid"), target.get("committedDate")
        if not (repo.get("nameWithOwner") and ref.get("name") and oid and committed
                and (repo.get("licenseInfo") or {}).get("spdxId")):
            raise SnapshotError(f"fixture {fixture} has an incomplete envelope")
        if not OID_RE.fullmatch(oid):
            raise SnapshotError(f"fixture {fixture} head oid is not a full hex object id")
        heads[fixture] = {"name_with_owner": repo["nameWithOwner"],
                          "default_branch": ref["name"], "head_oid": oid,
                          "committed_date": committed,
                          "license": repo["licenseInfo"]["spdxId"]}
    return {"heads": heads, "server_date_skew_seconds": round(skew, 3)}


def run(transport, now_fn, constants: dict, query_bytes: bytes) -> dict:
    cutoff = datetime.fromisoformat(constants["proposed_cutoff"].replace("Z", "+00:00"))
    skew_max = constants["max_clock_skew_seconds"]
    started = now_fn()
    window = abs((started - cutoff).total_seconds())
    receipt: dict = {
        "schema": RECEIPT_SCHEMA,
        "cutoff": constants["proposed_cutoff"],
        "request_started": started.isoformat(),
        "request_completed": None,
        "query_sha256": sha256_bytes(query_bytes),
        "response_sha256": None,
        "fire_window_seconds": round(window, 3),
    }
    try:
        if window > skew_max:
            raise SnapshotError(
                f"request start is {window:.1f}s from the sealed cutoff (max {skew_max}s)")
        token = os.environ.get("GITHUB_TOKEN")
        if not token:
            raise SnapshotError("GITHUB_TOKEN is not present in the environment")
        status, headers, body = transport(query_bytes, token)
        # Bind completion time and response digest the moment a response
        # exists, so even a rejected receipt carries them.
        completed = now_fn()
        receipt["request_completed"] = completed.isoformat()
        receipt["response_sha256"] = sha256_bytes(body)
        result = validate_response(status, headers, body, constants, completed)
        receipt.update(result)
        receipt["status"] = "ADMITTED"
        (STAGE1 / "response.json").write_bytes(body)
    except SnapshotError as exc:
        receipt["status"] = "REJECTED"
        receipt["failure"] = str(exc)[:500]
    except Exception as exc:  # one-shot executor: every failure seals a receipt
        receipt["status"] = "REJECTED"
        receipt["failure"] = f"unexpected failure: {type(exc).__name__}: {exc}"[:500]
    publish_receipt(receipt)
    return receipt


def publish_receipt(receipt: dict) -> None:
    """Durably publish the receipt; never raise.

    Atomic publication: temp file, fsync, rename, directory fsync. If
    serialization fails, a minimal rejection receipt is published instead.
    If filesystem publication fails entirely, the failure is recorded on
    the in-memory receipt ("receipt_publication_failure") so the caller
    emits it to stdout/stderr as the operator-preserved record.
    """
    try:
        try:
            payload = (json.dumps(receipt, sort_keys=True,
                                  separators=(",", ":")) + "\n").encode()
        except Exception as exc:
            minimal = {"schema": RECEIPT_SCHEMA, "status": "REJECTED",
                       "failure": f"receipt serialization failed: {type(exc).__name__}"}
            payload = (json.dumps(minimal, sort_keys=True,
                                  separators=(",", ":")) + "\n").encode()
        tmp = STAGE1 / "receipt.json.tmp"
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        try:
            os.write(fd, payload)
            os.fsync(fd)
        finally:
            os.close(fd)
        os.rename(tmp, STAGE1 / "receipt.json")
        dir_fd = os.open(STAGE1, os.O_RDONLY)
        try:
            os.fsync(dir_fd)
        finally:
            os.close(dir_fd)
    except Exception as exc:
        receipt["receipt_publication_failure"] = (
            f"{type(exc).__name__}: {exc}"[:300])


def main() -> int:
    parser = argparse.ArgumentParser()
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--wait", action="store_true")
    mode.add_argument("--now", action="store_true")
    args = parser.parse_args()

    query_bytes, constants = load_sealed_inputs()
    cutoff = datetime.fromisoformat(constants["proposed_cutoff"].replace("Z", "+00:00"))

    if not os.environ.get("GITHUB_TOKEN"):
        print("stage1: GITHUB_TOKEN missing — set it before the cutoff", file=sys.stderr)
        return 2
    if args.wait:
        delay = (cutoff - datetime.now(timezone.utc)).total_seconds()
        if delay > 0:
            print(f"stage1: sleeping {delay:.0f}s until sealed cutoff {constants['proposed_cutoff']}")
            time.sleep(delay)

    # Exclusive attempt marker: a prior stage1/ (admitted or rejected) blocks
    # re-firing; a new attempt requires a newly reviewed cutoff.
    try:
        STAGE1.mkdir(mode=0o700)
    except FileExistsError:
        print("stage1: an attempt already exists; re-firing requires a newly "
              "reviewed cutoff", file=sys.stderr)
        return 3

    receipt = run(default_transport, lambda: datetime.now(timezone.utc),
                  constants, query_bytes)
    print(json.dumps(receipt, indent=2, sort_keys=True))
    if "receipt_publication_failure" in receipt:
        # The printed copy above is the operator-preserved record.
        print("stage1: RECEIPT PUBLICATION FAILED — preserve this output",
              file=sys.stderr)
        print(json.dumps(receipt, sort_keys=True), file=sys.stderr)
        return 4
    return 0 if receipt["status"] == "ADMITTED" else 1


if __name__ == "__main__":
    raise SystemExit(main())
