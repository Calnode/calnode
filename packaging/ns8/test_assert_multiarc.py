"""Tests for the repository-argument handling in assert-multiarc.py.

Run offline, so they are safe to enforce on every push:

    python3 -m unittest discover -s packaging/ns8 -p 'test_*.py' -v

The registry round-trip tests are opt-in, because CI should not depend on
ghcr's availability to decide whether a source change is valid:

    NS8_REGISTRY_TESTS=1 python3 -m unittest discover -s packaging/ns8 -p 'test_*.py' -v

The four spellings below are the ones that matter. Three resolve and one does
not, and the one that does not is the exact string ns8-module.yml passes, which
made main red while the image itself was present and correct.
"""

import importlib.util
import os
import pathlib
import unittest

_SPEC = importlib.util.spec_from_file_location(
    "assert_multiarc",
    pathlib.Path(__file__).resolve().parent / "assert-multiarc.py",
)
am = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(am)


class NormalizeRepo(unittest.TestCase):
    def test_the_four_spellings_agree(self):
        """Every spelling of one repository must reduce to one canonical form.

        This is the regression. `ghcr.io/Calnode/ns8-calnode` used to 404 while
        the other three resolved, because a redundant leading host segment makes
        GitHub's edge router match the segments after it case-sensitively.
        """
        forms = [
            "calnode/ns8-calnode",
            "Calnode/ns8-calnode",
            "ghcr.io/calnode/ns8-calnode",
            "ghcr.io/Calnode/ns8-calnode",
        ]
        canonical = {am.normalize_repo(f) for f in forms}
        self.assertEqual(canonical, {"calnode/ns8-calnode"})

    def test_host_prefix_is_case_insensitive(self):
        for host in ("ghcr.io", "GHCR.IO", "Ghcr.io"):
            self.assertEqual(
                am.normalize_repo(f"{host}/Calnode/ns8-calnode"),
                "calnode/ns8-calnode",
            )

    def test_dotted_owner_is_not_mistaken_for_a_host(self):
        """A dotted first segment is a legal owner, not a registry host.

        Stripping it would silently rewrite `my.org/team/image` into a different
        repository, which is far worse than the 404 this function prevents.
        """
        self.assertEqual(am.normalize_repo("my.org/team/image"), "my.org/team/image")
        self.assertEqual(am.normalize_repo("My.Org/Team/Image"), "my.org/team/image")

    def test_two_segment_names_are_untouched_apart_from_case(self):
        self.assertEqual(am.normalize_repo("Calnode/Ns8-Calnode"), "calnode/ns8-calnode")

    def test_whitespace_is_trimmed(self):
        self.assertEqual(am.normalize_repo("  calnode/ns8-calnode\n"), "calnode/ns8-calnode")

    def test_normalisation_is_idempotent(self):
        once = am.normalize_repo("ghcr.io/Calnode/ns8-calnode")
        self.assertEqual(am.normalize_repo(once), once)

    def test_every_fetch_path_normalises(self):
        """get(), get_blob() and token() must all agree, not just one of them."""
        for name in ("get", "get_blob"):
            src = getattr(am, name).__doc__ or ""
            self.assertIn("normalize_repo", __import__("inspect").getsource(getattr(am, name)))
            del src
        self.assertIn("normalize_repo", __import__("inspect").getsource(am.token))


class RegistryRoundTrip(unittest.TestCase):
    """Opt-in: proves the four spellings produce identical live outcomes."""

    REPO = "calnode/ns8-calnode"
    TAG = os.environ.get("NS8_REGISTRY_TEST_TAG", "latest")

    def setUp(self):
        if not os.environ.get("NS8_REGISTRY_TESTS"):
            self.skipTest("set NS8_REGISTRY_TESTS=1 to run against ghcr.io")

    def test_all_four_spellings_resolve_to_the_same_index(self):
        bodies = {}
        for form in (
            self.REPO,
            "Calnode/ns8-calnode",
            f"ghcr.io/{self.REPO}",
            f"ghcr.io/Calnode/ns8-calnode",
        ):
            body, _ = am.get(form, self.TAG)
            bodies[form] = body
        digests = {form: body["manifests"][0]["digest"] for form, body in bodies.items()}
        self.assertEqual(
            len(set(digests.values())),
            1,
            f"the four spellings resolved to different images: {digests}",
        )

    def test_every_spelling_passes_the_real_check(self):
        for form in (
            self.REPO,
            "Calnode/ns8-calnode",
            f"ghcr.io/{self.REPO}",
            f"ghcr.io/Calnode/ns8-calnode",
        ):
            with self.subTest(repo=form):
                am.check_tag(form, self.TAG, ["amd64", "arm64"], False, None)


if __name__ == "__main__":
    unittest.main()