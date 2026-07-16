"""Synthetic-fixture tests for the burn-on-doubt carry-forward mapper."""

import os
import subprocess
import tempfile
import unittest

from carry_forward import classify


def _git(repo, *args):
    env = dict(os.environ,
               GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@t",
               GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@t",
               GIT_AUTHOR_DATE="2026-01-01T00:00:00Z",
               GIT_COMMITTER_DATE="2026-01-01T00:00:00Z")
    subprocess.run(["git", "-C", repo, *args], check=True, env=env,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


class CarryForwardTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.repo = self.dir.name
        _git(self.repo, "init", "-q")
        self._write("svc.go", "func Register(s *Server) {\n\tpb.Register(s)\n}\n")
        self._write("gone.go", "func Doomed() {}\n")
        self._write("moved.go", "func Carried() { carried() }\n")
        _git(self.repo, "add", "-A")
        _git(self.repo, "commit", "-qm", "old")
        self.old = self._head()

        # new head: svc.go edited but recognizable; gone.go deleted outright;
        # moved.go renamed with identical content.
        self._write("svc.go", "// comment\nfunc Register(s *Server) {\n\tpb.RegisterV2(s)\n}\n")
        os.remove(os.path.join(self.repo, "gone.go"))
        os.rename(os.path.join(self.repo, "moved.go"),
                  os.path.join(self.repo, "relocated.go"))
        _git(self.repo, "add", "-A")
        _git(self.repo, "commit", "-qm", "new")
        self.new = self._head()

    def tearDown(self):
        self.dir.cleanup()

    def _write(self, name, content):
        with open(os.path.join(self.repo, name), "w", encoding="utf-8") as f:
            f.write(content)

    def _head(self):
        out = subprocess.run(["git", "-C", self.repo, "rev-parse", "HEAD"],
                             check=True, stdout=subprocess.PIPE)
        return out.stdout.decode().strip()

    def test_identical_span_burns(self):
        decision = classify(self.repo, self.old, self.old, "svc.go", 1, 3)
        self.assertEqual((decision.decision, decision.rule), ("burn", "identical"))

    def test_edited_but_matching_declaration_burns(self):
        decision = classify(self.repo, self.old, self.new, "svc.go", 1, 3)
        self.assertEqual(decision.decision, "burn")
        self.assertIn(decision.rule, ("identical", "correspondence"))

    def test_deleted_file_is_free(self):
        decision = classify(self.repo, self.old, self.new, "gone.go", 1, 1)
        self.assertEqual((decision.decision, decision.rule), ("free", "absent"))

    def test_renamed_file_burns(self):
        decision = classify(self.repo, self.old, self.new, "moved.go", 1, 1)
        self.assertEqual((decision.decision, decision.rule),
                         ("burn", "renamed-successor"))

    def test_uncertain_defaults_to_burn(self):
        # Nonsense range: mapper must burn rather than guess.
        decision = classify(self.repo, self.old, self.new, "svc.go", 99, 120)
        self.assertEqual(decision.decision, "burn")


if __name__ == "__main__":
    unittest.main()
