from contextlib import nullcontext, redirect_stdout
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from scripts.release import ReleaseToolError, run_release, successful_release_run
from scripts.release_git import PreparedSource


class ReleaseWorkflowSelectionTest(unittest.TestCase):
    def test_simulation_finishes_without_tag_prompt_or_push(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            prepared = PreparedSource(directory, directory, "a" * 40, "v2.0.0-rc.31", {})
            with (
                patch("scripts.release.prepare_source", return_value=nullcontext(prepared)) as source,
                patch("scripts.release._report_directory", return_value=directory),
                patch("scripts.release._preflight"),
                patch("scripts.release._logged_command") as commands,
                patch("scripts.release.run_database_matrix"),
                patch("scripts.release.run_compose_smoke"),
                patch("scripts.release.run_mini_smoke"),
                patch("scripts.release.subprocess.run"),
                patch("scripts.release.prompt_for_tag", side_effect=AssertionError("tag prompt")),
                patch("scripts.release.publish_tag", side_effect=AssertionError("tag push")),
                redirect_stdout(io.StringIO()),
            ):
                self.assertEqual(run_release(directory, simulate=True), 0)
            source.assert_called_once_with(directory, local_head=True)
            report = json.loads((directory / "summary.json").read_text())
            self.assertEqual(report["simulation_status"], "passed")
            self.assertIs(report["tag_pushed"], False)
            self.assertEqual(
                [call.args[1] for call in commands.call_args_list],
                ["build-native", "build-image"],
            )

    def test_tag_push_hands_off_to_existing_release_without_waiting(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            prepared = PreparedSource(directory, directory, "a" * 40, "v2.0.0-rc.31", {})
            with (
                patch("scripts.release.prepare_source", return_value=nullcontext(prepared)),
                patch("scripts.release._report_directory", return_value=directory),
                patch("scripts.release._preflight"),
                patch("scripts.release._logged_command"),
                patch("scripts.release.run_database_matrix"),
                patch("scripts.release.run_compose_smoke"),
                patch("scripts.release.run_mini_smoke"),
                patch("scripts.release.subprocess.run"),
                patch("scripts.release._command", return_value="one commit"),
                patch("scripts.release.prompt_for_tag", return_value="v2.0.0-rc.32"),
                patch("scripts.release.publish_tag") as publish,
                patch("scripts.release._release_runs", side_effect=AssertionError("release polling")),
                redirect_stdout(io.StringIO()),
            ):
                self.assertEqual(run_release(directory), 0)
            publish.assert_called_once_with(prepared, "v2.0.0-rc.32")
            report = json.loads((directory / "summary.json").read_text())
            self.assertIs(report["tag_pushed"], True)
            self.assertEqual(report["release_status"], "not_checked")

    def test_previous_release_requires_completed_success_on_exact_tag(self):
        payload = {
            "workflow_runs": [
                {"id": 1, "head_branch": "v2.0.0-rc.31", "status": "completed", "conclusion": "failure"},
                {"id": 2, "head_branch": "main", "status": "completed", "conclusion": "success"},
                {"id": 3, "head_branch": "v2.0.0-rc.31", "status": "completed", "conclusion": "success"},
            ]
        }
        self.assertEqual(successful_release_run(payload, "v2.0.0-rc.31"), 3)
        with self.assertRaises(ReleaseToolError):
            successful_release_run(payload, "v2.0.0-rc.32")

if __name__ == "__main__":
    unittest.main()
