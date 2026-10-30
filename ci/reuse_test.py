import unittest
import argparse
import subprocess
import os
import tempfile
from pathlib import Path
from datetime import datetime, timezone
from unittest.mock import patch

import reuse


class ReuseTests(unittest.TestCase):
    def test_only_non_runtime_changes_are_ignored(self):
        for name in ["README.md", "docs/usage.md", "internal/update/user_stories_test.go", "data/namenlos_test.go", "ci/reuse_test.py"]:
            self.assertFalse(reuse.relevant(name), name)
        for name in ["internal/update/update.go", "data/thirdparty/pins.env", "data/examples/demo_test.go", "data/networks/n/.generated/keys/key.pem", "data/networks/n/.generated/releases/v1/SHA256SUMS", ".github/workflows/ci.yml", "ci/reuse.py", "Makefile", "scripts/gates.sh", "unknown-file"]:
            self.assertTrue(reuse.relevant(name), name)

    def test_key_uses_actual_blob_and_mode(self):
        def key(tree):
            with patch.object(reuse, "command", side_effect=lambda *a: tree if a[1] == "ls-tree" else b""):
                return reuse.input_key("HEAD")
        base = b"100644 blob a\tinternal/run.go\0"
        self.assertEqual(key(base), key(base + b"100644 blob b\tREADME.md\0"))
        self.assertEqual(key(base), key(base + b"100644 blob c\tinternal/run_test.go\0"))
        self.assertNotEqual(key(base), key(base.replace(b"blob a", b"blob d")))
        self.assertNotEqual(key(base), key(base.replace(b"100644", b"100755")))

    def test_embedded_test_named_files_are_runtime_inputs(self):
        base = b"100644 blob a\tinternal/runtime/fixture_test.go\0"
        def key(tree):
            with patch.object(reuse, "command", side_effect=lambda *a: tree if a[1] == "ls-tree" else b"HEAD:internal/runtime/embed.go\n"):
                return reuse.input_key("HEAD")
        self.assertNotEqual(key(base), key(base.replace(b"blob a", b"blob b")))

    def test_only_same_repo_main_or_current_pr_provides_evidence(self):
        run = {"id": 1, "head_repository": {"full_name": "katzenpost/kno"}, "event": "pull_request", "head_branch": "fix"}
        self.assertTrue(reuse.trusted(run, "katzenpost/kno", "fix", "2"))
        self.assertFalse(reuse.trusted(run, "katzenpost/kno", "other", "2"))
        self.assertFalse(reuse.trusted(run, "katzenpost/kno", "fix", "1"))
        run["head_repository"]["full_name"] = "fork/kno"
        self.assertFalse(reuse.trusted(run, "katzenpost/kno", "fix", "2"))
        run.update(head_repository={"full_name": "katzenpost/kno"}, event="push", head_branch="main")
        self.assertTrue(reuse.trusted(run, "katzenpost/kno", "fix", "2"))

    def test_main_can_reuse_its_merged_pr_but_not_unrelated_history(self):
        run = {"id": 1, "head_repository": {"full_name": "katzenpost/kno"}, "event": "pull_request", "head_branch": "fix", "head_sha": "merged"}
        with patch.object(reuse, "command", return_value=b"") as git:
            self.assertTrue(reuse.trusted(run, "katzenpost/kno", "main", "2"))
            git.assert_called_once_with("git", "merge-base", "--is-ancestor", "merged", "HEAD")
        with patch.object(reuse, "command", side_effect=subprocess.CalledProcessError(1, "git")):
            self.assertFalse(reuse.trusted(run, "katzenpost/kno", "main", "2"))

    def test_reused_hashes_are_actual_outputs_and_conflicts_refused(self):
        a, b = "a" * 64, "b" * 64
        log = f"2026-10-30T20:00:00Z {a}  /tmp/pkgs/katzenpost_1_amd64.deb\n{b}  namenlos-config-x-1.pkg\n{a}  kno_1_amd64.deb\n"
        self.assertEqual(reuse.package_sums(log), f"{a}  katzenpost_1_amd64.deb\n{b}  namenlos-config-x-1.pkg")
        with self.assertRaises(ValueError):
            reuse.package_sums(log + f"{b}  katzenpost_1_amd64.deb\n")
        with self.assertRaises(ValueError):
            reuse.package_sums("no hashes\n")


    def test_find_never_reuses_failed_or_changed_jobs(self):
        run = {"id": 1, "head_repository": {"full_name": "katzenpost/kno"}, "event": "pull_request", "head_branch": "fix", "head_sha": "old", "created_at": datetime.now(timezone.utc).isoformat()}
        args = argparse.Namespace(workflow="ci.yml", job="sim", sums=False)
        env = {"GITHUB_REPOSITORY": "katzenpost/kno", "GITHUB_HEAD_REF": "fix", "GITHUB_REF_NAME": "fix", "GITHUB_RUN_ID": "2"}
        for conclusion, old_key, expected in [("success", "same", True), ("failure", "same", False), ("success", "changed", False)]:
            jobs = {"jobs": [{"id": 101, "name": "sim", "conclusion": conclusion, "html_url": "https://github.com/katzenpost/kno/actions/jobs/1"}]}
            with patch.dict(os.environ, env, clear=True), patch.object(reuse, "api", side_effect=[{"workflow_runs": [run]}, jobs]), patch.object(reuse, "input_key", side_effect=["same", old_key]), patch.object(reuse, "command", return_value=b"[command]/usr/bin/git log -1 --format=%H\n" + b"f" * 40 + b"\n"):
                self.assertEqual(reuse.find(args) is not None, expected)

    def test_evidence_uses_tested_merge_tree_not_pr_head(self):
        sha = "f" * 40
        log = "[command]/usr/bin/git log -1 --format=%H\n2026-10-30T20:00:00Z " + sha + "\n"
        self.assertEqual(reuse.checked_out_commit(log), sha)
        with self.assertRaises(ValueError):
            reuse.checked_out_commit("HEAD is now at abc123 PR head")

    def test_manual_runs_and_api_failures_run_every_step(self):
        for event in ["workflow_dispatch", "pull_request"]:
            with tempfile.TemporaryDirectory() as tmp:
                output = Path(tmp) / "output"
                env = {"GITHUB_EVENT_NAME": event, "GITHUB_OUTPUT": str(output)}
                argv = ["reuse.py", "--workflow", "ci.yml", "--job", "sim"]
                with patch.dict(os.environ, env, clear=True), patch("sys.argv", argv), patch.object(reuse, "find", side_effect=ValueError("API unavailable")) as find:
                    reuse.main()
                self.assertEqual(output.read_text(), "reused=false\n")
                self.assertEqual(find.called, event == "pull_request")


if __name__ == "__main__":
    unittest.main()
