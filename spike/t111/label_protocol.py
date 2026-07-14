"""Canonical Gate 2 label validation, freezing, and publication receipts.

This module has no command-line or network behavior.  Callers supply label
rows, the exact selected site-ID population, and (for GitHub verification) a
fetch function.  Every serialized byte is therefore reproducible and suitable
for an external commitment before scoring.
"""

from __future__ import annotations

import datetime as dt
import hashlib
import json
import os
import re
from collections.abc import Callable, Iterable, Mapping, Sequence
from pathlib import Path
from typing import Any, Final
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import Request, urlopen


LABEL_FIELDS: Final = frozenset(
    {
        "site_id",
        "invocation",
        "operation",
        "registration",
        "service",
        "expected_code_role",
        "rationale",
        "evidence",
    }
)
DECISIONS: Final = frozenset({"yes", "no", "unsure"})
CODE_ROLES: Final = frozenset(
    {"production", "test", "mock", "generated", "vendor"}
)

LABEL_COMMITMENT_SCHEMA: Final = "t111-label-commitment-v1"
LABEL_COMMITMENT_FIELDS: Final = frozenset(
    {
        "schema",
        "labels_sha256",
        "label_count",
        "site_ids_sha256",
        "input_binding",
        "artifact_manifest_sha256",
        "reviewer_assignment_sha256",
        "adjudication",
        "committed_at",
    }
)

GITHUB_GIST_RECEIPT_SCHEMA: Final = "t111-github-initial-gist-receipt-v1"
GITHUB_GIST_RECEIPT_FIELDS: Final = frozenset(
    {"schema", "api_url", "revision", "created_at", "document_sha256"}
)

_SHA256_RE = re.compile(r"sha256:[0-9a-f]{64}")
_REVISION_RE = re.compile(r"[0-9a-f]{40}")
_RFC3339_UTC_RE = re.compile(
    r"[0-9]{4}-(?:0[1-9]|1[0-2])-"
    r"(?:0[1-9]|[12][0-9]|3[01])T"
    r"(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]Z"
)
_OPERATION_RE = re.compile(
    r"[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+"
    r"/[A-Za-z_][A-Za-z0-9_]*"
)
_SERVICE_RE = re.compile(
    r"[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+"
)
_GIST_PATH_RE = re.compile(r"/gists/[0-9A-Za-z]+")
_NIST_BEACON_PATH_RE = re.compile(
    r"/beacon/2\.0/(?:pulse/time/[0-9]{1,16}|chain/[0-9]+/pulse/[0-9]+)"
)


class LabelProtocolError(ValueError):
    """A label document or publication receipt violates the fixed protocol."""


def sha256_bytes(data: bytes) -> str:
    """Return the protocol's tagged SHA-256 representation."""

    if not isinstance(data, bytes):
        raise TypeError("sha256_bytes requires bytes")
    return "sha256:" + hashlib.sha256(data).hexdigest()


def validate_labels(
    rows: Iterable[Mapping[str, Any]], known_ids: Iterable[str]
) -> tuple[dict[str, Any], ...]:
    """Validate a complete label population and return it sorted by site ID.

    The equality check is intentional: a frozen label document may contain
    neither an extra coordinate nor an omitted selected coordinate.
    """

    expected = _validate_known_ids(known_ids)
    validated: dict[str, dict[str, Any]] = {}
    for index, raw in enumerate(rows):
        if not isinstance(raw, Mapping):
            raise LabelProtocolError(f"label row {index} must be an object")
        if set(raw) != LABEL_FIELDS:
            raise LabelProtocolError(
                f"label row {index} fields must be exactly {sorted(LABEL_FIELDS)}"
            )
        row = dict(raw)
        site_id = _canonical_site_id(row["site_id"], f"label row {index}")
        if site_id in validated:
            raise LabelProtocolError(f"duplicate label site_id: {site_id}")

        invocation = _decision(row["invocation"], site_id, "invocation")
        operation = row["operation"]
        if invocation == "yes":
            if not isinstance(operation, str) or not _OPERATION_RE.fullmatch(
                operation
            ):
                raise LabelProtocolError(
                    f"{site_id}: invocation=yes requires canonical "
                    "package.Service/Method"
                )
        elif operation is not None:
            raise LabelProtocolError(
                f"{site_id}: invocation={invocation} requires operation=null"
            )

        registration = _decision(row["registration"], site_id, "registration")
        service = row["service"]
        if registration == "yes":
            if not isinstance(service, str) or not _SERVICE_RE.fullmatch(service):
                raise LabelProtocolError(
                    f"{site_id}: registration=yes requires canonical package.Service"
                )
        elif service is not None:
            raise LabelProtocolError(
                f"{site_id}: registration={registration} requires service=null"
            )

        role = row["expected_code_role"]
        if not isinstance(role, str) or role not in CODE_ROLES:
            raise LabelProtocolError(
                f"{site_id}: expected_code_role must be one of {sorted(CODE_ROLES)}"
            )
        for field in ("rationale", "evidence"):
            value = row[field]
            if not isinstance(value, str) or not value.strip():
                raise LabelProtocolError(
                    f"{site_id}: {field} must be a nonempty string"
                )

        # Force all custom Mapping implementations into ordinary JSON objects
        # before canonical serialization.
        validated[site_id] = row

    actual = set(validated)
    if actual != expected:
        missing = sorted(expected - actual)
        unknown = sorted(actual - expected)
        details: list[str] = []
        if missing:
            details.append(f"missing={missing[:3]}")
        if unknown:
            details.append(f"unknown={unknown[:3]}")
        raise LabelProtocolError(
            "label site IDs must exactly equal known site IDs"
            + (": " + ", ".join(details) if details else "")
        )
    return tuple(validated[site_id] for site_id in sorted(validated))


def canonical_labels_jsonl(
    rows: Iterable[Mapping[str, Any]], known_ids: Iterable[str]
) -> bytes:
    """Return validated, site-ID-sorted canonical UTF-8 JSONL bytes."""

    validated = validate_labels(rows, known_ids)
    try:
        return b"".join(_canonical_json_line(row) for row in validated)
    except (TypeError, UnicodeEncodeError, ValueError) as exc:
        raise LabelProtocolError(f"labels are not canonical JSON data: {exc}") from exc


def canonical_labels_sha256(
    rows: Iterable[Mapping[str, Any]], known_ids: Iterable[str]
) -> str:
    """Hash the exact canonical label document."""

    return sha256_bytes(canonical_labels_jsonl(rows, known_ids))


def freeze_labels(
    path: str | os.PathLike[str],
    rows: Iterable[Mapping[str, Any]],
    known_ids: Iterable[str],
) -> str:
    """Create a mode-0600 canonical label file exactly once.

    ``O_EXCL`` is the protocol invariant: an existing freeze is never replaced,
    even when its bytes would be identical.  A failed first write is removed so
    it cannot be mistaken for a complete commitment.
    """

    document = canonical_labels_jsonl(rows, known_ids)
    freeze_bytes_exclusive(path, document)
    return sha256_bytes(document)


def freeze_bytes_exclusive(
    path: str | os.PathLike[str], document: bytes
) -> None:
    """Persist immutable bytes with ``O_EXCL`` and exact mode 0600."""

    if not isinstance(document, bytes):
        raise TypeError("freeze_bytes_exclusive requires bytes")
    destination = Path(path)
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    fd: int | None = None
    created = False
    try:
        fd = os.open(destination, flags, 0o600)
        created = True
        os.fchmod(fd, 0o600)
        view = memoryview(document)
        written = 0
        while written < len(view):
            count = os.write(fd, view[written:])
            if count <= 0:
                raise OSError("short write while freezing document")
            written += count
        os.fsync(fd)
        os.close(fd)
        fd = None
        _fsync_directory(destination.parent)
    except Exception:
        if fd is not None:
            os.close(fd)
        if created:
            try:
                destination.unlink()
            except FileNotFoundError:
                pass
        raise


def build_label_commitment(
    rows: Iterable[Mapping[str, Any]],
    known_ids: Iterable[str],
    committed_at: str,
    *,
    input_binding: str,
    artifact_manifest_sha256: str,
    reviewer_assignment_sha256: str,
    adjudication: Mapping[str, Any],
) -> dict[str, Any]:
    """Build a validated commitment to one complete canonical label document."""

    known = _validate_known_ids(known_ids)
    document = canonical_labels_jsonl(rows, known)
    value = {
        "schema": LABEL_COMMITMENT_SCHEMA,
        "labels_sha256": sha256_bytes(document),
        "label_count": len(known),
        "site_ids_sha256": _site_ids_sha256(known),
        "input_binding": input_binding,
        "artifact_manifest_sha256": artifact_manifest_sha256,
        "reviewer_assignment_sha256": reviewer_assignment_sha256,
        "adjudication": dict(adjudication),
        "committed_at": committed_at,
    }
    return validate_label_commitment(value)


def validate_label_commitment(value: Mapping[str, Any]) -> dict[str, Any]:
    """Validate and copy the exact v1 label-commitment schema."""

    if not isinstance(value, Mapping) or set(value) != LABEL_COMMITMENT_FIELDS:
        raise LabelProtocolError(
            "label commitment fields must be exactly "
            f"{sorted(LABEL_COMMITMENT_FIELDS)}"
        )
    result = dict(value)
    if result["schema"] != LABEL_COMMITMENT_SCHEMA:
        raise LabelProtocolError("unsupported label commitment schema")
    _tagged_sha256(result["labels_sha256"], "labels_sha256")
    _tagged_sha256(result["site_ids_sha256"], "site_ids_sha256")
    _tagged_sha256(result["input_binding"], "input_binding")
    _tagged_sha256(result["artifact_manifest_sha256"], "artifact_manifest_sha256")
    _tagged_sha256(result["reviewer_assignment_sha256"], "reviewer_assignment_sha256")
    count = result["label_count"]
    if isinstance(count, bool) or not isinstance(count, int) or count < 0:
        raise LabelProtocolError("label_count must be a nonnegative integer")
    adjudication = result["adjudication"]
    if not isinstance(adjudication, Mapping) or set(adjudication) != {
        "overlap_sites", "disagreements", "adjudicated", "unresolved"
    }:
        raise LabelProtocolError("adjudication must contain the exact fixed counters")
    for field, value in adjudication.items():
        if isinstance(value, bool) or not isinstance(value, int) or value < 0:
            raise LabelProtocolError(f"adjudication.{field} must be a nonnegative integer")
    if adjudication["disagreements"] > adjudication["overlap_sites"]:
        raise LabelProtocolError("adjudication disagreements exceed overlap sites")
    if adjudication["adjudicated"] < adjudication["disagreements"]:
        raise LabelProtocolError("every reviewer disagreement must be adjudicated")
    if adjudication["unresolved"] != 0:
        raise LabelProtocolError("frozen Gate-2 labels cannot remain unresolved")
    result["adjudication"] = dict(adjudication)
    _rfc3339_utc(result["committed_at"], "committed_at")
    return result


def canonical_label_commitment_document(value: Mapping[str, Any]) -> bytes:
    """Return the exact one-line document intended for external publication."""

    commitment = validate_label_commitment(value)
    return _canonical_json_line(commitment)


def verify_label_commitment(
    value: Mapping[str, Any],
    rows: Iterable[Mapping[str, Any]],
    known_ids: Iterable[str],
) -> dict[str, Any]:
    """Verify a commitment against the complete canonical label population."""

    commitment = validate_label_commitment(value)
    known = _validate_known_ids(known_ids)
    document = canonical_labels_jsonl(rows, known)
    expected = {
        "labels_sha256": sha256_bytes(document),
        "label_count": len(known),
        "site_ids_sha256": _site_ids_sha256(known),
    }
    for field, expected_value in expected.items():
        if commitment[field] != expected_value:
            raise LabelProtocolError(
                f"label commitment {field} does not match canonical labels"
            )
    return commitment


def github_json_fetcher(url: str) -> Mapping[str, Any]:
    """Fetch one public GitHub API object with bounded transport and payload size."""

    _github_gist_api_or_revision_url(url)
    request = Request(
        url,
        headers={
            "Accept": "application/vnd.github+json",
            "User-Agent": "phebs-t111-gate2-auditor",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    try:
        with urlopen(request, timeout=20) as response:
            if response.status != 200:
                raise LabelProtocolError(
                    f"GitHub API returned unexpected HTTP {response.status}"
                )
            payload = response.read(8 * 1024 * 1024 + 1)
    except (HTTPError, URLError, OSError) as exc:
        raise LabelProtocolError(f"fetch GitHub Gist: {exc}") from exc
    if len(payload) > 8 * 1024 * 1024:
        raise LabelProtocolError("GitHub Gist API response exceeds 8 MiB")
    try:
        value = json.loads(payload)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise LabelProtocolError(f"GitHub Gist response is not valid JSON: {exc}") from exc
    if not isinstance(value, Mapping):
        raise LabelProtocolError("GitHub Gist response must be an object")
    return value


def nist_beacon_json_fetcher(url: str) -> Mapping[str, Any]:
    """Fetch one exact NIST Beacon 2.0 pulse with bounded transport.

    Only the documented time lookup and immutable canonical chain/pulse
    endpoints are accepted.  In particular, callers cannot silently fall back
    to ``pulse/last`` if the protocol's precommitted pulse is unavailable.
    """

    _nist_beacon_api_url(url)
    request = Request(
        url,
        headers={
            "Accept": "application/json",
            "User-Agent": "phebs-t111-gate2-auditor",
        },
    )
    try:
        with urlopen(request, timeout=20) as response:
            if response.geturl() != url:
                raise LabelProtocolError("NIST Beacon API redirected the exact pulse URL")
            if response.status != 200:
                raise LabelProtocolError(
                    f"NIST Beacon API returned unexpected HTTP {response.status}"
                )
            payload = response.read(2 * 1024 * 1024 + 1)
    except (HTTPError, URLError, OSError) as exc:
        raise LabelProtocolError(f"fetch NIST Beacon pulse: {exc}") from exc
    if len(payload) > 2 * 1024 * 1024:
        raise LabelProtocolError("NIST Beacon API response exceeds 2 MiB")
    try:
        value = json.loads(payload)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise LabelProtocolError(
            f"NIST Beacon response is not valid JSON: {exc}"
        ) from exc
    if not isinstance(value, Mapping):
        raise LabelProtocolError("NIST Beacon response must be an object")
    return value


def validate_github_gist_receipt(value: Mapping[str, Any]) -> dict[str, Any]:
    """Validate and copy an initial-Gist-revision publication receipt."""

    if not isinstance(value, Mapping) or set(value) != GITHUB_GIST_RECEIPT_FIELDS:
        raise LabelProtocolError(
            "GitHub Gist receipt fields must be exactly "
            f"{sorted(GITHUB_GIST_RECEIPT_FIELDS)}"
        )
    result = dict(value)
    if result["schema"] != GITHUB_GIST_RECEIPT_SCHEMA:
        raise LabelProtocolError("unsupported GitHub Gist receipt schema")
    _github_gist_api_url(result["api_url"])
    revision = result["revision"]
    if not isinstance(revision, str) or not _REVISION_RE.fullmatch(revision):
        raise LabelProtocolError("revision must be a full lowercase Git revision")
    _rfc3339_utc(result["created_at"], "created_at")
    _tagged_sha256(result["document_sha256"], "document_sha256")
    return result


GistFetcher = Callable[[str], Mapping[str, Any]]


def build_github_initial_gist_receipt(
    api_url: str,
    document: bytes,
    fetcher: GistFetcher,
) -> dict[str, Any]:
    """Discover and verify the initial revision of a newly published Gist."""

    _github_gist_api_url(api_url)
    base = _fetch_gist(fetcher, api_url, "Gist")
    history = base.get("history")
    if (
        not isinstance(history, Sequence)
        or isinstance(history, (str, bytes))
        or not history
        or not isinstance(history[-1], Mapping)
    ):
        raise LabelProtocolError("GitHub Gist has no initial history revision")
    receipt = {
        "schema": GITHUB_GIST_RECEIPT_SCHEMA,
        "api_url": api_url,
        "revision": history[-1].get("version"),
        "created_at": base.get("created_at"),
        "document_sha256": sha256_bytes(document),
    }
    checked = validate_github_gist_receipt(receipt)
    verify_github_initial_gist_receipt(checked, document, fetcher)
    return checked


def verify_github_initial_gist_receipt(
    receipt: Mapping[str, Any],
    document: bytes,
    fetcher: GistFetcher,
) -> dict[str, Any]:
    """Verify that ``document`` is the one file in a Gist's first revision.

    ``fetcher`` is called with the receipt's base API URL and then the exact
    revision URL.  It must return decoded GitHub API objects.  Keeping network
    transport outside this module makes the verification deterministic and
    straightforward to test without credentials or live services.
    """

    checked = validate_github_gist_receipt(receipt)
    if not isinstance(document, bytes):
        raise TypeError("document must be bytes")
    actual_digest = sha256_bytes(document)
    if actual_digest != checked["document_sha256"]:
        raise LabelProtocolError(
            "receipt document_sha256 does not match the supplied document bytes"
        )
    base = _fetch_gist(fetcher, checked["api_url"], "Gist")
    if base.get("url") != checked["api_url"]:
        raise LabelProtocolError("GitHub Gist URL does not match receipt api_url")
    if base.get("created_at") != checked["created_at"]:
        raise LabelProtocolError("GitHub created_at does not match receipt")

    history = base.get("history")
    if not isinstance(history, Sequence) or isinstance(history, (str, bytes)):
        raise LabelProtocolError("GitHub Gist history must be an array")
    if not history:
        raise LabelProtocolError("GitHub Gist history is empty")
    for index, entry in enumerate(history):
        if not isinstance(entry, Mapping):
            raise LabelProtocolError(f"GitHub history entry {index} is not an object")
        revision = entry.get("version")
        committed_at = entry.get("committed_at")
        if not isinstance(revision, str) or not _REVISION_RE.fullmatch(revision):
            raise LabelProtocolError(
                f"GitHub history entry {index} has an invalid revision"
            )
        _rfc3339_utc(committed_at, f"history[{index}].committed_at")

    initial = history[-1]
    if initial.get("version") != checked["revision"]:
        raise LabelProtocolError("receipt revision is not the initial Gist revision")
    if initial.get("committed_at") != checked["created_at"]:
        raise LabelProtocolError(
            "initial Gist revision time does not equal GitHub created_at"
        )
    created = _parse_rfc3339_utc(checked["created_at"], "created_at")
    if any(
        _parse_rfc3339_utc(entry["committed_at"], "history committed_at") < created
        for entry in history[:-1]
    ):
        raise LabelProtocolError("GitHub history contains a revision before created_at")

    revision_url = checked["api_url"] + "/" + checked["revision"]
    revision_data = _fetch_gist(fetcher, revision_url, "Gist revision")
    files = revision_data.get("files")
    if not isinstance(files, Mapping) or len(files) != 1:
        raise LabelProtocolError("initial Gist revision must contain exactly one file")
    filename, file_data = next(iter(files.items()))
    if not isinstance(filename, str) or not filename:
        raise LabelProtocolError("initial Gist file has an invalid name")
    if not isinstance(file_data, Mapping):
        raise LabelProtocolError("initial Gist file metadata is not an object")
    if file_data.get("truncated") is not False:
        raise LabelProtocolError("initial Gist file content is truncated or unverified")
    content = file_data.get("content")
    if not isinstance(content, str):
        raise LabelProtocolError("initial Gist file has no inline UTF-8 content")
    try:
        published = content.encode("utf-8", errors="strict")
    except UnicodeEncodeError as exc:
        raise LabelProtocolError("initial Gist content is not valid UTF-8") from exc
    if published != document:
        raise LabelProtocolError(
            "initial Gist file bytes do not equal the committed document"
        )
    if sha256_bytes(published) != checked["document_sha256"]:
        raise LabelProtocolError("initial Gist file digest does not match receipt")
    return checked


def _validate_known_ids(known_ids: Iterable[str]) -> set[str]:
    if isinstance(known_ids, (str, bytes)):
        raise LabelProtocolError("known_ids must be an iterable of site IDs")
    result: set[str] = set()
    for index, raw in enumerate(known_ids):
        site_id = _canonical_site_id(raw, f"known_ids[{index}]")
        if site_id in result:
            raise LabelProtocolError(f"duplicate known site_id: {site_id}")
        result.add(site_id)
    return result


def _canonical_site_id(value: Any, context: str) -> str:
    if (
        not isinstance(value, str)
        or not value
        or value != value.strip()
        or any(ord(char) < 0x20 or ord(char) == 0x7F for char in value)
    ):
        raise LabelProtocolError(f"{context}: site_id must be a canonical string")
    return value


def _decision(value: Any, site_id: str, field: str) -> str:
    if not isinstance(value, str) or value not in DECISIONS:
        raise LabelProtocolError(f"{site_id}: {field} must be yes/no/unsure")
    return value


def _canonical_json_line(value: Mapping[str, Any]) -> bytes:
    encoded = json.dumps(
        value,
        ensure_ascii=False,
        allow_nan=False,
        sort_keys=True,
        separators=(",", ":"),
    )
    return (encoded + "\n").encode("utf-8", errors="strict")


def _site_ids_sha256(site_ids: Iterable[str]) -> str:
    document = _canonical_json_line({"site_ids": sorted(site_ids)})
    return sha256_bytes(document)


def _tagged_sha256(value: Any, field: str) -> str:
    if not isinstance(value, str) or not _SHA256_RE.fullmatch(value):
        raise LabelProtocolError(f"{field} must be sha256:<64 lowercase hex>")
    return value


def _rfc3339_utc(value: Any, field: str) -> str:
    _parse_rfc3339_utc(value, field)
    return value


def _parse_rfc3339_utc(value: Any, field: str) -> dt.datetime:
    if not isinstance(value, str) or not _RFC3339_UTC_RE.fullmatch(value):
        raise LabelProtocolError(f"{field} must be canonical RFC3339 UTC seconds")
    try:
        parsed = dt.datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ")
    except ValueError as exc:
        raise LabelProtocolError(f"{field} is not a real UTC timestamp") from exc
    return parsed.replace(tzinfo=dt.timezone.utc)


def _github_gist_api_url(value: Any) -> str:
    if not isinstance(value, str):
        raise LabelProtocolError("api_url must be a GitHub HTTPS API URL")
    parsed = urlsplit(value)
    if (
        parsed.scheme != "https"
        or parsed.netloc != "api.github.com"
        or not _GIST_PATH_RE.fullmatch(parsed.path)
        or parsed.query
        or parsed.fragment
    ):
        raise LabelProtocolError(
            "api_url must be https://api.github.com/gists/<gist-id>"
        )
    return value


def _github_gist_api_or_revision_url(value: Any) -> str:
    if not isinstance(value, str):
        raise LabelProtocolError("GitHub URL must be HTTPS API text")
    parsed = urlsplit(value)
    if (
        parsed.scheme != "https"
        or parsed.netloc != "api.github.com"
        or not re.fullmatch(r"/gists/[0-9A-Za-z]+(?:/[0-9a-f]{40})?", parsed.path)
        or parsed.query
        or parsed.fragment
    ):
        raise LabelProtocolError("GitHub URL is not a canonical Gist API URL")
    return value


def _nist_beacon_api_url(value: Any) -> str:
    if not isinstance(value, str):
        raise LabelProtocolError("NIST Beacon URL must be HTTPS API text")
    parsed = urlsplit(value)
    if (
        parsed.scheme != "https"
        or parsed.netloc != "beacon.nist.gov"
        or not _NIST_BEACON_PATH_RE.fullmatch(parsed.path)
        or parsed.query
        or parsed.fragment
    ):
        raise LabelProtocolError(
            "NIST Beacon URL must be an exact time or canonical chain/pulse endpoint"
        )
    return value


def _fetch_gist(
    fetcher: GistFetcher, url: str, description: str
) -> Mapping[str, Any]:
    try:
        value = fetcher(url)
    except Exception as exc:
        raise LabelProtocolError(f"fetch {description}: {exc}") from exc
    if not isinstance(value, Mapping):
        raise LabelProtocolError(f"{description} response must be an object")
    return value


def _fsync_directory(directory: Path) -> None:
    flags = os.O_RDONLY
    if hasattr(os, "O_DIRECTORY"):
        flags |= os.O_DIRECTORY
    fd = os.open(directory, flags)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)
