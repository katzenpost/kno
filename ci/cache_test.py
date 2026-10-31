import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


def load(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + ".py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class BuildCacheTests(unittest.TestCase):
    def test_only_builder_images_are_exported(self):
        module = load("images")
        rows = [{"Names": ["localhost/kno-build-debian13-go1.26.10:latest", "localhost/kno-sim-client:latest"]},
                {"Names": ["localhost/kno-katzenpost-deb-debian13-amd64-go1.26.10:latest"]},
                {"Names": None}, {"Names": ["docker.io/library/debian:bookworm-slim"]}]
        with patch.object(module.subprocess, "check_output", return_value=json.dumps(rows).encode()):
            self.assertEqual(len(module.images()), 2)
            self.assertTrue(all("go1.26.10" in name for name in module.images()))

    def test_restore_releases_archive_only_after_successful_load(self):
        module = load("images")
        for fail in (False, True):
            with self.subTest(fail=fail), tempfile.TemporaryDirectory() as directory:
                root = Path(directory) / ".cache/katzenpost/ci-images"
                root.mkdir(parents=True)
                archive = root / "builders.tar"
                archive.write_bytes(b"cached builders")
                error = subprocess.CalledProcessError(1, "podman") if fail else None
                with patch.object(module.Path, "home", return_value=Path(directory)), \
                        patch.object(sys, "argv", ["images.py", "restore"]), \
                        patch.object(module.subprocess, "run", side_effect=error) as run, \
                        contextlib.redirect_stdout(io.StringIO()):
                    if fail:
                        with self.assertRaises(subprocess.CalledProcessError):
                            module.main()
                    else:
                        module.main()
                    run.assert_called_once_with(["podman", "load", "--input", str(archive)], check=True)
                self.assertEqual(archive.exists(), fail)

    def test_failed_export_does_not_replace_the_previous_cache(self):
        module = load("images")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / ".cache/katzenpost/ci-images"
            root.mkdir(parents=True)
            archive = root / "builders.tar"
            archive.write_bytes(b"previous")
            with patch.object(module.Path, "home", return_value=Path(directory)), \
                    patch.object(module, "images", return_value=["localhost/kno-build-test"]), \
                    patch.object(sys, "argv", ["images.py", "save"]), \
                    patch.object(module.subprocess, "run", side_effect=subprocess.CalledProcessError(1, "podman")):
                with self.assertRaises(subprocess.CalledProcessError):
                    module.main()
            self.assertEqual(archive.read_bytes(), b"previous")

    def test_stage_timing_keeps_failure_status_and_records_duration(self):
        module = load("timed")
        with tempfile.TemporaryDirectory() as directory:
            summary = Path(directory) / "summary"
            with patch.dict(os.environ, {"GITHUB_STEP_SUMMARY": str(summary)}), \
                    patch.object(sys, "argv", ["timed.py", "consensus", "test-command"]), \
                    patch.object(module.subprocess, "run", return_value=subprocess.CompletedProcess([], 7)), \
                    contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(module.main(), 7)
            self.assertIn("consensus:", summary.read_text())
            self.assertIn("(exit 7)", summary.read_text())


if __name__ == "__main__":
    unittest.main()
