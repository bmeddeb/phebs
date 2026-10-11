from __future__ import annotations

import unittest

import corpus


def repo(**changes):
    value = {
        "name": "example.com/app",
        "git_url": "https://example.com/app.git",
        "commit": "a" * 40,
        "license": {"spdx": "Apache-2.0", "source": "recorded at pin"},
        "inputs": {"index.scip": {"bytes": 3, "sha256": "sha256:" + "b" * 64}},
    }
    value.update(changes)
    return value


def lock(**changes):
    value = corpus.build_lock("2026-10-10T00:00:00Z", "rule", [repo()])
    value.update(changes)
    return value


class BuildLockTest(unittest.TestCase):
    def test_build_is_sorted_and_rebuild_is_byte_identical(self):
        first = corpus.build_lock("2026-10-10T00:00:00Z", "rule", [repo(), repo(name="example.com/z", commit="c" * 40)])
        second = corpus.build_lock("2026-10-10T00:00:00Z", "rule", [repo(name="example.com/z", commit="c" * 40), repo()])
        self.assertEqual([r["name"] for r in first["repositories"]], ["example.com/app", "example.com/z"])
        self.assertEqual(corpus.canonical_bytes(first), corpus.canonical_bytes(second))
        self.assertEqual(corpus.digest_of(first), corpus.digest_of(second))

    def test_refuses_inconsistent_selections(self):
        cases = {
            "bad utc": {"created_utc": "2026-10-10", "selection_rule": "r", "repositories": [repo()]},
            "empty rule": {"created_utc": "2026-10-10T00:00:00Z", "selection_rule": " ", "repositories": [repo()]},
            "no repositories": {"created_utc": "2026-10-10T00:00:00Z", "selection_rule": "r", "repositories": []},
            "duplicate names": {"created_utc": "2026-10-10T00:00:00Z", "selection_rule": "r",
                                "repositories": [repo(), repo()]},
            "upper-case commit": {"created_utc": "2026-10-10T00:00:00Z", "selection_rule": "r",
                                  "repositories": [repo(commit="A" * 40)]},
            "short commit": {"created_utc": "2026-10-10T00:00:00Z", "selection_rule": "r",
                             "repositories": [repo(commit="a" * 39)]},
            "no license": {"created_utc": "2026-10-10T00:00:00Z", "selection_rule": "r",
                           "repositories": [repo(license={"spdx": "", "source": "x"})]},
            "no inputs": {"created_utc": "2026-10-10T00:00:00Z", "selection_rule": "r",
                          "repositories": [repo(inputs={})]},
            "absolute input path": {"created_utc": "2026-10-10T00:00:00Z", "selection_rule": "r",
                                    "repositories": [repo(inputs={"/index.scip": {"bytes": 1, "sha256": "sha256:" + "b" * 64}})]},
            "parent input path": {"created_utc": "2026-10-10T00:00:00Z", "selection_rule": "r",
                                  "repositories": [repo(inputs={"../index.scip": {"bytes": 1, "sha256": "sha256:" + "b" * 64}})]},
            "negative bytes": {"created_utc": "2026-10-10T00:00:00Z", "selection_rule": "r",
                               "repositories": [repo(inputs={"index.scip": {"bytes": -1, "sha256": "sha256:" + "b" * 64}})]},
            "bool bytes": {"created_utc": "2026-10-10T00:00:00Z", "selection_rule": "r",
                           "repositories": [repo(inputs={"index.scip": {"bytes": True, "sha256": "sha256:" + "b" * 64}})]},
            "bad input digest": {"created_utc": "2026-10-10T00:00:00Z", "selection_rule": "r",
                                 "repositories": [repo(inputs={"index.scip": {"bytes": 1, "sha256": "sha256:" + "b" * 63}})]},
            "unknown repo field": {"created_utc": "2026-10-10T00:00:00Z", "selection_rule": "r",
                                   "repositories": [repo(extra=1)]},
        }
        for name, kwargs in cases.items():
            with self.subTest(name=name):
                with self.assertRaises(corpus.CorpusError):
                    corpus.build_lock(**kwargs)


class ValidateLockTest(unittest.TestCase):
    def test_accepts_built_lock(self):
        corpus.validate_lock(lock())

    def test_refuses_edited_locks(self):
        two = corpus.build_lock("2026-10-10T00:00:00Z", "rule", [repo(), repo(name="example.com/z", commit="c" * 40)])
        cases = {
            "unknown field": {**lock(), "extra": 1},
            "unsorted": lock(repositories=list(reversed(two["repositories"]))),
            "wrong schema": lock(schema="other"),
            "empty repositories": lock(repositories=[]),
        }
        for name, value in cases.items():
            with self.subTest(name=name):
                with self.assertRaises(corpus.CorpusError):
                    corpus.validate_lock(value)


class VerifyLockTest(unittest.TestCase):
    def test_matching_observation_passes(self):
        corpus.verify_lock(lock(), [repo()])

    def test_any_divergence_refuses(self):
        cases = {
            "commit changed": [repo(commit="d" * 40)],
            "url changed": [repo(git_url="https://example.com/other.git")],
            "license changed": [repo(license={"spdx": "MIT", "source": "recorded at pin"})],
            "extra input": [repo(inputs={"index.scip": {"bytes": 3, "sha256": "sha256:" + "b" * 64},
                                         "extra.json": {"bytes": 1, "sha256": "sha256:" + "e" * 64}})],
            "no declared inputs": [repo(inputs={})],
            "bytes changed": [repo(inputs={"index.scip": {"bytes": 4, "sha256": "sha256:" + "b" * 64}})],
            "digest changed": [repo(inputs={"index.scip": {"bytes": 3, "sha256": "sha256:" + "f" * 64}})],
            "renamed repository": [repo(name="example.com/other")],
            "extra repository": [repo(), repo(name="example.com/z", commit="c" * 40)],
            "no repositories observed": [],
        }
        for name, observed in cases.items():
            with self.subTest(name=name):
                with self.assertRaises(corpus.CorpusError):
                    corpus.verify_lock(lock(), observed)

    def test_duplicate_observation_refuses(self):
        with self.assertRaises(corpus.CorpusError):
            corpus.verify_lock(lock(), [repo(), repo()])


class RunBindingTest(unittest.TestCase):
    def test_binding_binds_lock_digest_and_commits(self):
        value = corpus.run_binding(lock(), "1" * 40)
        self.assertEqual(value["schema"], corpus.BINDING_SCHEMA)
        self.assertEqual(value["corpus_lock_sha256"], corpus.digest_of(lock()))
        self.assertEqual(value["repositories"], [{"name": "example.com/app", "commit": "a" * 40}])
        corpus.validate_binding(value)

    def test_refuses_bad_inputs(self):
        with self.assertRaises(corpus.CorpusError):
            corpus.run_binding(lock(), "1" * 39)
        cases = {
            "missing field": {k: v for k, v in corpus.run_binding(lock(), "1" * 40).items() if k != "repositories"},
            "bad digest": {**corpus.run_binding(lock(), "1" * 40), "corpus_lock_sha256": "sha256:" + "x" * 64},
            "empty repositories": {**corpus.run_binding(lock(), "1" * 40), "repositories": []},
        }
        for name, value in cases.items():
            with self.subTest(name=name):
                with self.assertRaises(corpus.CorpusError):
                    corpus.validate_binding(value)


if __name__ == "__main__":
    unittest.main()
