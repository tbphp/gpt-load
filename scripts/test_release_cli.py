import unittest

from scripts.release import ReleaseToolError, successful_release_run, started_release_run


class ReleaseWorkflowSelectionTest(unittest.TestCase):
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

    def test_new_release_run_is_selected_only_for_target_tag(self):
        payload = {
            "workflow_runs": [
                {"id": 10, "head_branch": "main", "status": "completed", "conclusion": "success"},
                {"id": 11, "head_branch": "v2.0.0-rc.32", "status": "queued", "conclusion": None},
            ]
        }
        self.assertEqual(started_release_run(payload, "v2.0.0-rc.32"), 11)
        self.assertIsNone(started_release_run(payload, "v2.0.0-rc.33"))


if __name__ == "__main__":
    unittest.main()
