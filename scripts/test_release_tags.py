import unittest

from scripts.release_tags import (
    ReleaseTagError,
    parse_remote_tags,
    select_latest_tag,
    suggest_next_tag,
    validate_new_tag,
    verify_local_remote_tags,
)


class ReleaseTagsTest(unittest.TestCase):
    def test_remote_tags_ignore_annotated_peeled_refs(self):
        refs = (
            "aaa\trefs/tags/v2.0.0-rc.9\n"
            "bbb\trefs/tags/v2.0.0-rc.10\n"
            "ccc\trefs/tags/v2.0.0-rc.10^{}\n"
        )
        self.assertEqual(
            parse_remote_tags(refs),
            {"v2.0.0-rc.9": "aaa", "v2.0.0-rc.10": "bbb"},
        )

    def test_latest_tag_uses_semver_not_ref_order(self):
        self.assertEqual(
            select_latest_tag(
                {"v2.0.0-rc.9": "a", "v2.0.0": "b", "v2.0.0-rc.31": "c"}
            ),
            "v2.0.0",
        )

    def test_suggestions_only_advance_an_explicit_release_line(self):
        self.assertEqual(suggest_next_tag("v2.0.0-rc.31"), "v2.0.0-rc.32")
        self.assertEqual(suggest_next_tag("v2.0.0-beta.9"), "v2.0.0-beta.10")
        self.assertEqual(suggest_next_tag("v2.0.0"), "v2.0.1")
        with self.assertRaises(ReleaseTagError):
            suggest_next_tag("v2.0.0-preview.1")

    def test_stable_tag_after_rc_is_valid_but_existing_or_older_is_not(self):
        existing = {"v2.0.0-rc.31": "a"}
        validate_new_tag("v2.0.0", "v2.0.0-rc.31", existing)
        for proposed in ("v2.0.0-rc.31", "v2.0.0-rc.30", "v3.0.0", "2.0.0"):
            with self.subTest(proposed=proposed), self.assertRaises(ReleaseTagError):
                validate_new_tag(proposed, "v2.0.0-rc.31", existing)

    def test_same_name_local_divergence_fails_without_discarding_local_tags(self):
        with self.assertRaises(ReleaseTagError):
            verify_local_remote_tags(
                {"v2.0.0-rc.31": "local"}, {"v2.0.0-rc.31": "remote"}
            )
        with self.assertRaises(ReleaseTagError):
            verify_local_remote_tags(
                {"v2.0.0-rc.32": "local"}, {"v2.0.0-rc.31": "remote"}
            )


if __name__ == "__main__":
    unittest.main()
