import copy
import json
from pathlib import Path
import unittest

import prepare


class FrozenInputTests(unittest.TestCase):
    def setUp(self):
        self.lock = json.loads((Path(__file__).parent / "population.lock.json").read_text())

    def test_actual_population_is_well_formed(self):
        prepare.validate_lock(self.lock)

    def test_bad_population_refuses(self):
        cases = (
            ("schema", lambda d: d.update(schema="unknown")),
            ("moving-ref", lambda d: d["upstream"].update(commit="main")),
            ("duplicate", lambda d: d["files"].append(copy.deepcopy(d["files"][0]))),
            ("escape", lambda d: d["files"][0].update(path="../secret")),
            ("foreign-url", lambda d: d["files"][0].update(url="https://example.invalid/data")),
            ("oversize", lambda d: d["files"][0].update(bytes=1048577)),
            ("bad-digest", lambda d: d["files"][0].update(sha256="sha256:" + "g" * 64)),
            ("root-mapping", lambda d: d["files"][0].update(derived_path="changed")),
        )
        for name, mutate in cases:
            with self.subTest(name=name):
                changed = copy.deepcopy(self.lock)
                mutate(changed)
                with self.assertRaises(ValueError):
                    prepare.validate_lock(changed)

    def test_changed_bytes_and_label_spans_refuse(self):
        raw = b"c.Call()\n"
        row = {"bytes": len(raw), "sha256": prepare.digest(raw), "git_blob": "0" * 40}
        with self.assertRaises(ValueError):
            prepare.verify_bytes(row, raw)
        label = {
            "site_id": "call", "upstream_path": "main.go",
            "start_byte": 0, "end_byte": 6, "selector": "c.Call",
            "start_line": 1, "end_line": 1, "upstream_blob_sha256": prepare.digest(raw),
        }
        labels = {"schema": "phebs-t503-source-labels-v1", "sites": [label]}
        prepare.verify_labels(labels, {"main.go": raw})
        for key, value in (("start_byte", 1), ("end_byte", 7), ("start_line", 2), ("selector", "c.Other")):
            with self.subTest(key=key):
                altered = copy.deepcopy(labels)
                altered["sites"][0][key] = value
                with self.assertRaises(ValueError):
                    prepare.verify_labels(altered, {"main.go": raw})


if __name__ == "__main__":
    unittest.main()
