#!/usr/bin/env python3
"""Focused tests for bounded-memory pinned Git blob streaming."""

from __future__ import annotations

import sys
import unittest
from pathlib import Path
from typing import Callable
from unittest.mock import patch


sys.path.insert(0, str(Path(__file__).resolve().parent))

import label_prep as prep  # noqa: E402


class _FakeInput:
    def __init__(self, release_response: Callable[[], None]) -> None:
        self.writes: list[bytes] = []
        self.closed = False
        self._release_response = release_response

    def write(self, value: bytes) -> int:
        self.writes.append(value)
        return len(value)

    def flush(self) -> None:
        self._release_response()

    def close(self) -> None:
        self.closed = True


class _ResponseStream:
    def __init__(self, responses: list[bytes]) -> None:
        self._responses = list(responses)
        self._available = bytearray()
        self.closed = False

    def release_next(self) -> None:
        if not self._responses:
            return
        if self._available:
            raise AssertionError("next response requested before the prior response was consumed")
        self._available.extend(self._responses.pop(0))

    def read(self, size: int = -1) -> bytes:
        if size < 0 or size > len(self._available):
            size = len(self._available)
        result = bytes(self._available[:size])
        del self._available[:size]
        return result

    def close(self) -> None:
        self.closed = True


class _FakeProcess:
    def __init__(self, responses: list[bytes], *, returncode: int = 0) -> None:
        self.stdout = _ResponseStream(responses)
        self.stdin = _FakeInput(self.stdout.release_next)
        self.returncode = returncode
        self.killed = False
        self.wait_calls = 0

    def poll(self) -> int | None:
        if self.stdin.closed or self.killed:
            return self.returncode
        return None

    def wait(self, timeout: float | None = None) -> int:
        del timeout
        self.wait_calls += 1
        return self.returncode

    def kill(self) -> None:
        self.killed = True
        self.returncode = -9


def _record(payload: bytes, *, object_type: bytes = b"blob") -> bytes:
    return b"a" * 40 + b" " + object_type + b" " + str(len(payload)).encode() + b"\0" + payload + b"\0"


def _regular_tree(paths: list[str]) -> dict[str, tuple[str, str, str]]:
    return {
        path: ("100644", "blob", f"{index:040x}")
        for index, path in enumerate(paths, start=1)
    }


class GitBatchStreamTests(unittest.TestCase):
    def test_rejects_duplicate_and_unsafe_paths_before_starting_git(self) -> None:
        root = Path("/corpus")
        with patch.object(prep.subprocess, "Popen") as popen:
            with self.assertRaisesRegex(prep.PrepError, "duplicate path"):
                prep.git_blobs(root, "1" * 40, ["a.go", "a.go"])
            for rel in ("", "/absolute.go", "../escape.go", "a/../escape.go", "nul\0name.go"):
                with self.subTest(rel=rel):
                    with self.assertRaisesRegex(prep.PrepError, "unsafe pinned path"):
                        prep.git_blobs(root, "1" * 40, [rel])
            popen.assert_not_called()

    def test_streams_one_nul_safe_request_at_a_time_without_retaining_prior_blob(self) -> None:
        first_payload = b"first payload"
        second_payload = b"second payload"
        process = _FakeProcess([_record(first_payload), _record(second_payload)])
        paths = ["dir/a file.go", "dir/line\nbreak.go"]
        commit = "2" * 40

        tree = _regular_tree(paths)
        with patch.object(prep, "pinned_tree_entries", return_value=tree), patch.object(
            prep.subprocess, "Popen", return_value=process
        ) as popen:
            blobs = prep.git_blobs(Path("/corpus"), commit, paths)
            first = next(blobs)
            self.assertEqual(first, (paths[0], first_payload))
            self.assertEqual(process.stdin.writes, [tree[paths[0]][2].encode() + b"\0"])

            second = next(blobs)
            self.assertEqual(second, (paths[1], second_payload))
            self.assertEqual(
                process.stdin.writes,
                [tree[path][2].encode() + b"\0" for path in paths],
            )
            self.assertIsNotNone(blobs.gi_frame)
            self.assertFalse(
                any(value is first[1] for value in blobs.gi_frame.f_locals.values()),
                "streaming iterator retained a previously yielded blob",
            )

            with self.assertRaises(StopIteration):
                next(blobs)

        popen.assert_called_once_with(
            ["git", "-C", "/corpus", "cat-file", "--batch", "-Z"],
            stdin=prep.subprocess.PIPE,
            stdout=prep.subprocess.PIPE,
            stderr=unittest.mock.ANY,
        )
        self.assertTrue(process.stdin.closed)
        self.assertTrue(process.stdout.closed)
        self.assertEqual(process.wait_calls, 1)

    def test_rejects_malformed_truncated_and_missing_responses(self) -> None:
        cases = (
            ("malformed header", b"not-a-batch-header\0", "did not resolve a regular blob"),
            ("wrong object type", _record(b"body", object_type=b"tree"), "did not resolve a regular blob"),
            ("missing blob", b"1" * 40 + b":missing.go missing\0", "did not resolve a regular blob"),
            ("truncated header", b"a" * 40 + b" blob 3", "truncated Git batch header"),
            ("truncated content", b"a" * 40 + b" blob 3\0ab", "truncated Git batch content"),
            ("missing content terminator", b"a" * 40 + b" blob 3\0abc", "truncated Git batch content"),
        )
        for name, output, message in cases:
            with self.subTest(name=name):
                process = _FakeProcess([output])
                with patch.object(
                    prep, "pinned_tree_entries", return_value=_regular_tree(["a.go"])
                ), patch.object(prep.subprocess, "Popen", return_value=process):
                    with self.assertRaisesRegex(prep.PrepError, message):
                        list(prep.git_blobs(Path("/corpus"), "3" * 40, ["a.go"]))
                self.assertTrue(process.stdin.closed)
                self.assertTrue(process.stdout.closed)

    def test_rejects_trailing_output_and_nonzero_git_exit(self) -> None:
        trailing = _FakeProcess([_record(b"body") + b"unexpected"])
        with patch.object(
            prep, "pinned_tree_entries", return_value=_regular_tree(["a.go"])
        ), patch.object(prep.subprocess, "Popen", return_value=trailing):
            with self.assertRaisesRegex(prep.PrepError, "emitted trailing data"):
                list(prep.git_blobs(Path("/corpus"), "4" * 40, ["a.go"]))
        self.assertTrue(trailing.killed)

        failed = _FakeProcess([_record(b"body")], returncode=7)
        with patch.object(
            prep, "pinned_tree_entries", return_value=_regular_tree(["a.go"])
        ), patch.object(prep.subprocess, "Popen", return_value=failed):
            with self.assertRaisesRegex(prep.PrepError, "git cat-file batch failed"):
                list(prep.git_blobs(Path("/corpus"), "4" * 40, ["a.go"]))

    def test_registration_scan_consumes_streamed_blobs(self) -> None:
        blobs = iter(
            (
                ("a.go", b"package p\nfunc wire() { pb.RegisterAlphaServer(s, a) }\n"),
                ("b.go", b"package p\nfunc wire() { s.RegisterService(d, b) }\n"),
            )
        )
        with patch.object(prep, "git_blobs", return_value=blobs):
            rows = prep.scan_registration_recall_frame(
                "fixture", Path("/corpus"), "5" * 40, ["b.go", "a.go"]
            )
        self.assertEqual([row["path"] for row in rows], ["a.go", "b.go"])
        self.assertEqual(
            [row["method"] for row in rows],
            ["RegisterAlphaServer", "RegisterService"],
        )


if __name__ == "__main__":
    unittest.main()
