from contextlib import redirect_stdout
import io
import subprocess
import tempfile
import unittest
from pathlib import Path

from scripts.release_git import (
    ReleaseCancelled,
    ReleaseSourceError,
    prepare_source,
    prompt_for_tag,
    publish_tag,
    verify_source_unchanged,
)


def git(cwd: Path, *args: str) -> str:
    return subprocess.run(
        ["git", *args], cwd=cwd, check=True, capture_output=True, text=True
    ).stdout.strip()


class ReleaseSourceTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.remote = self.root / "remote.git"
        self.source = self.root / "source"
        self.remote.mkdir()
        self.source.mkdir()
        git(self.remote, "init", "--bare", "-q")
        git(self.source, "init", "-q", "-b", "main")
        git(self.source, "config", "user.name", "Release Test")
        git(self.source, "config", "user.email", "release@example.test")
        git(self.source, "remote", "add", "origin", str(self.remote))
        (self.source / "tracked").write_text("first\n")
        git(self.source, "add", "tracked")
        git(self.source, "commit", "-qm", "first")
        git(self.source, "tag", "v2.0.0-rc.1")
        git(self.source, "push", "-q", "-u", "origin", "main", "--tags")

    def test_prepares_remote_main_without_changing_dirty_caller(self):
        (self.source / "tracked").write_text("local edit\n")
        (self.source / "untracked").write_text("local file\n")
        with prepare_source(self.source) as prepared:
            self.assertEqual(prepared.candidate_sha, git(self.source, "rev-parse", "origin/main"))
            self.assertEqual(prepared.latest_tag, "v2.0.0-rc.1")
            self.assertEqual((prepared.worktree / "tracked").read_text(), "first\n")
            self.assertEqual((self.source / "tracked").read_text(), "local edit\n")
            self.assertTrue((self.source / "untracked").exists())
            verify_source_unchanged(prepared)
            worktree = prepared.worktree
        self.assertFalse(worktree.exists())

    def test_remote_main_advancing_invalidates_checked_candidate(self):
        with prepare_source(self.source) as prepared:
            (self.source / "tracked").write_text("second\n")
            git(self.source, "add", "tracked")
            git(self.source, "commit", "-qm", "second")
            git(self.source, "push", "-q", "origin", "main")
            with self.assertRaises(ReleaseSourceError):
                verify_source_unchanged(prepared)

    def test_simulation_uses_committed_local_head_without_pushing_it(self):
        (self.source / "tracked").write_text("local candidate\n")
        git(self.source, "add", "tracked")
        git(self.source, "commit", "-qm", "local candidate")
        local_head = git(self.source, "rev-parse", "HEAD")
        remote_main = git(self.source, "ls-remote", "origin", "refs/heads/main").split()[0]

        with prepare_source(self.source, local_head=True) as prepared:
            self.assertEqual(prepared.candidate_sha, local_head)
            self.assertEqual((prepared.worktree / "tracked").read_text(), "local candidate\n")
            self.assertNotEqual(prepared.candidate_sha, remote_main)

        self.assertEqual(git(self.source, "ls-remote", "origin", "refs/heads/main").split()[0], remote_main)

    def test_simulation_rejects_uncommitted_changes(self):
        (self.source / "tracked").write_text("uncommitted\n")
        with self.assertRaisesRegex(ReleaseSourceError, "未提交"):
            with prepare_source(self.source, local_head=True):
                pass

    def test_tag_is_pushed_only_after_explicit_confirmation(self):
        self._push_second_commit()
        with prepare_source(self.source) as prepared:
            answers = iter(["", "yes"])
            with redirect_stdout(io.StringIO()):
                tag = prompt_for_tag(prepared, input_fn=lambda _: next(answers))
            self.assertEqual(tag, "v2.0.0-rc.2")
            self.assertEqual(git(self.source, "tag", "--list", tag), "")
            publish_tag(prepared, tag)
            self.assertEqual(
                git(self.source, "ls-remote", "origin", f"refs/tags/{tag}").split()[0],
                prepared.candidate_sha,
            )

    def test_declined_confirmation_creates_no_tag(self):
        self._push_second_commit()
        with prepare_source(self.source) as prepared:
            answers = iter(["", "no"])
            with self.assertRaises(ReleaseCancelled), redirect_stdout(io.StringIO()):
                prompt_for_tag(prepared, input_fn=lambda _: next(answers))
            self.assertEqual(git(self.source, "tag", "--list", "v2.0.0-rc.2"), "")

    def test_unknown_prerelease_requires_manual_version_instead_of_guessing(self):
        self._push_second_commit()
        git(self.source, "tag", "v2.0.0-zz.1")
        git(self.source, "push", "-q", "origin", "refs/tags/v2.0.0-zz.1")
        with prepare_source(self.source) as prepared:
            self.assertEqual(prepared.latest_tag, "v2.0.0-zz.1")
            answers = iter(["v2.0.0", "yes"])
            with redirect_stdout(io.StringIO()):
                self.assertEqual(
                    prompt_for_tag(prepared, input_fn=lambda _: next(answers)), "v2.0.0"
                )

    def _push_second_commit(self):
        (self.source / "tracked").write_text("second\n")
        git(self.source, "add", "tracked")
        git(self.source, "commit", "-qm", "second")
        git(self.source, "push", "-q", "origin", "main")


if __name__ == "__main__":
    unittest.main()
