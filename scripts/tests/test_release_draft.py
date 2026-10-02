import importlib.util
from pathlib import Path
import unittest

path = Path(__file__).resolve().parents[1] / "prepare-release-draft.py"
spec = importlib.util.spec_from_file_location("release_draft", path)
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class ReleaseDestinationTests(unittest.TestCase):
    def test_bridge_stays_discoverable_by_old_clients(self):
        for tag in ["0.5.33-Beta", "v0.5.39-Beta", "0.5.40-Beta", "0.5.40"]:
            with self.subTest(tag=tag):
                self.assertEqual(release.release_destination(tag), release.LEGACY)

    def test_future_releases_never_enter_the_legacy_catalog(self):
        for tag in ["0.5.41-Beta", "v0.5.42", "0.5.100", "0.6.0", "1.0.0"]:
            with self.subTest(tag=tag):
                self.assertEqual(release.release_destination(tag), release.MODERN)

    def test_rejects_arbitrary_refs_and_options(self):
        for tag in ["main", "--repo", "release/0.5.41", "0.5.41 Beta", "0.5.41\n", ""]:
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                release.release_destination(tag)


if __name__ == "__main__":
    unittest.main()
