"""Run with python3 scripts/restart_claude_supervisors_test.py."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent


class RestartClaudeSupervisorsTest(unittest.TestCase):
    def run_target(self, sessions="", list_exit=0, kill_exit=0, legacy_sessions=""):
        with tempfile.TemporaryDirectory() as directory:
            fake = Path(directory) / "tmux"
            fake.write_text(
                "#!/bin/sh\n"
                'test "$1" = -L || exit 99\n'
                'case "$2" in ocman-agents) names="$SESSIONS" ;; ocman-provider) names="$LEGACY_SESSIONS" ;; *) exit 99 ;; esac\n'
                'case "$3" in\n'
                'list-sessions) printf "%s" "$names"; exit "$LIST_EXIT" ;;\n'
                'kill-session) printf "%s %s\\n" "$2" "$5" >> "$CALLS"; exit "$KILL_EXIT" ;;\n'
                "*) exit 99 ;;\n"
                "esac\n"
            )
            fake.chmod(0o700)
            calls = Path(directory) / "calls"
            result = subprocess.run(
                ["make", "--no-print-directory", "restart-claude-supervisors"],
                cwd=ROOT,
                env={
                    **os.environ,
                    "PATH": directory + os.pathsep + os.environ["PATH"],
                    "SESSIONS": sessions,
                    "LEGACY_SESSIONS": legacy_sessions,
                    "LIST_EXIT": str(list_exit),
                    "KILL_EXIT": str(kill_exit),
                    "CALLS": str(calls),
                },
                capture_output=True,
                text=True,
            )
            return result, calls.read_text().splitlines() if calls.exists() else []

    def test_only_claude_supervisors(self):
        result, calls = self.run_target(
            "opencode-project\nclaudecode-root-1\nocman-term\nclaudecode-root-2\n"
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, ["ocman-agents =claudecode-root-1", "ocman-agents =claudecode-root-2"])
        self.assertIn("next create/send will relaunch", result.stdout)

    def test_no_server_is_a_noop(self):
        result, calls = self.run_target(list_exit=1)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, [])

    def test_kill_failure_is_reported(self):
        result, calls = self.run_target("claudecode-root-1\nclaudecode-root-2\n", kill_exit=1)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, ["ocman-agents =claudecode-root-1"])
        self.assertNotIn("Stopped", result.stdout)

    def test_legacy_server_only(self):
        result, calls = self.run_target(legacy_sessions="opencode-project\nclaudecode-old\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, ["ocman-provider =claudecode-old"])

    def test_legacy_kill_failure_is_reported(self):
        result, calls = self.run_target(legacy_sessions="claudecode-old\n", kill_exit=1)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, ["ocman-provider =claudecode-old"])


if __name__ == "__main__":
    unittest.main()
