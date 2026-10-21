"""Tests for the clone-module step.

Run offline, so a broken invariant fails on every push:

    python3 -m unittest discover -s packaging/ns8 -p 'test_*.py' -v

These cover the one thing that must never go wrong: a cloned instance must give
up the hostname without losing the encryption key that makes its copied database
readable.
"""

import importlib.util
import json
import os
import pathlib
import tempfile
import unittest

_STEP = pathlib.Path(__file__).resolve().parent / (
    "imageroot/actions/clone-module/20unclaimhostname"
)
_SPEC = importlib.util.spec_from_loader(
    "unclaim", importlib.machinery.SourceFileLoader("unclaim", str(_STEP))
)
unclaim = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(unclaim)


def write_env(path, values, comments=True):
    lines = ["# Written by configure-module."] if comments else []
    lines += [f"{k}={v}" for k, v in values.items()]
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")
    os.chmod(path, 0o600)


def write_config(path, host):
    path.write_text(
        json.dumps({"host": host, "lets_encrypt": True, "http2https": True}),
        encoding="utf-8",
    )


class ReleaseHostname(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.config = pathlib.Path(self.dir.name) / "config.json"
        self.env = pathlib.Path(self.dir.name) / "calnode.env"

    def values(self):
        return unclaim.read_env_file(str(self.env))

    def test_encryption_key_survives_and_hostname_does_not(self):
        write_env(
            self.env,
            {
                "PORT": "3000",
                "DATABASE_URL": "sqlite:///data/calnode.db",
                "BASE_URL": "https://book.example.com",
                "CALNODE_ENCRYPTION_KEY": "abc123",
            },
        )
        write_config(self.config, "book.example.com")

        unclaim.release_hostname(str(self.config), str(self.env))

        values = self.values()
        self.assertNotIn("BASE_URL", values)
        self.assertEqual(values["CALNODE_ENCRYPTION_KEY"], "abc123")
        self.assertEqual(values["PORT"], "3000")
        self.assertEqual(values["DATABASE_URL"], "sqlite:///data/calnode.db")

    def test_config_is_removed_so_the_ui_asks_for_a_hostname(self):
        write_env(self.env, {"BASE_URL": "https://x.example.com", "CALNODE_ENCRYPTION_KEY": "k"})
        write_config(self.config, "x.example.com")

        removed, _ = unclaim.release_hostname(str(self.config), str(self.env))

        self.assertTrue(removed)
        self.assertFalse(self.config.exists())

    def test_missing_config_is_not_an_error(self):
        """A clone of an unconfigured instance has nothing to release."""
        write_env(self.env, {"PORT": "3000", "CALNODE_ENCRYPTION_KEY": "k"})
        removed, dropped = unclaim.release_hostname(str(self.config), str(self.env))
        self.assertFalse(removed)
        self.assertEqual(dropped, [])
        self.assertEqual(self.values()["CALNODE_ENCRYPTION_KEY"], "k")

    def test_missing_env_file_is_not_an_error(self):
        write_config(self.config, "x.example.com")
        removed, dropped = unclaim.release_hostname(str(self.config), str(self.env))
        self.assertTrue(removed)
        self.assertEqual(dropped, [])
        self.assertFalse(self.config.exists())

    def test_refuses_to_write_an_env_file_without_the_key(self):
        """Better to fail the clone than to hand back an unreadable database."""
        write_env(self.env, {"BASE_URL": "https://x.example.com"})
        with self.assertRaises(RuntimeError) as caught:
            unclaim.release_hostname(str(self.config), str(self.env))
        self.assertIn("CALNODE_ENCRYPTION_KEY", str(caught.exception))

    def test_refusal_leaves_the_files_untouched(self):
        """The guard must fire before anything is rewritten.

        Checking after the write would abort a step that had already mutated the
        clone, so it would differ from its source with nothing recording it.
        """
        write_env(self.env, {"BASE_URL": "https://x.example.com", "PORT": "3000"})
        write_config(self.config, "x.example.com")
        before_env = self.env.read_text()
        before_config = self.config.read_text()

        with self.assertRaises(RuntimeError):
            unclaim.release_hostname(str(self.config), str(self.env))

        self.assertEqual(self.env.read_text(), before_env)
        self.assertEqual(self.config.read_text(), before_config)

    def test_env_file_stays_owner_only(self):
        write_env(self.env, {"BASE_URL": "https://x.example.com", "CALNODE_ENCRYPTION_KEY": "k"})
        unclaim.release_hostname(str(self.config), str(self.env))
        self.assertEqual(os.stat(self.env).st_mode & 0o777, 0o600)

    def test_comments_and_blank_lines_are_tolerated(self):
        self.env.write_text(
            "# Written by configure-module.\n\n"
            "PORT=3000\n\n# a note\nBASE_URL=https://x.example.com\n"
            "CALNODE_ENCRYPTION_KEY=k\n",
            encoding="utf-8",
        )
        unclaim.release_hostname(str(self.config), str(self.env))
        values = self.values()
        self.assertNotIn("BASE_URL", values)
        self.assertEqual(values["CALNODE_ENCRYPTION_KEY"], "k")
        self.assertEqual(values["PORT"], "3000")

    def test_is_idempotent(self):
        """clone-module steps can be retried; running twice must not lose more."""
        write_env(self.env, {"BASE_URL": "https://x.example.com", "CALNODE_ENCRYPTION_KEY": "k"})
        write_config(self.config, "x.example.com")
        unclaim.release_hostname(str(self.config), str(self.env))
        unclaim.release_hostname(str(self.config), str(self.env))
        self.assertEqual(self.values()["CALNODE_ENCRYPTION_KEY"], "k")


if __name__ == "__main__":
    unittest.main()