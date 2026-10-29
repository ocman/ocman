#!/usr/bin/env python3
"""Exercise release tag selection with output larger than a pipe buffer."""
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent


class TagSelectionTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        git = Path(self.directory.name) / "git"
        # Restore normal SIGPIPE handling: an early reader exit must fail the
        # producer exactly as Git can on the Linux CI runner. No real tags or
        # credentials are touched. rev-list is empty (no SDK changes).
        git.write_text("""#!/usr/bin/env python3
import os
import signal
import sys
signal.signal(signal.SIGPIPE, signal.SIG_DFL)
if os.environ.get('TAG_TEST_GIT_FAILURE'):
    sys.exit(42)
if sys.argv[1] == 'tag':
    prefix = 'sdk/' if 'sdk/v*' in sys.argv else ''
    print(prefix + 'v0.9.0', flush=True)
    for i in range(100000):
        print(prefix + 'v0.0.' + str(i))
""")
        git.chmod(0o755)
        self.env = dict(os.environ, PATH=self.directory.name + os.pathsep + os.environ["PATH"])

    def test_app_tag_selection_consumes_all_output(self):
        workflow = (ROOT / ".forgejo/workflows/ci.yml").read_text()
        expression = re.search(r'^\s*(latest_tag=".*")$', workflow, re.MULTILINE)
        self.assertIsNotNone(expression)
        result = subprocess.run(
            ["bash", "-euo", "pipefail", "-c", expression[1] + '\nprintf "%s" "$latest_tag"'],
            env=self.env, capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "v0.9.0")

    def test_sdk_tag_selection_consumes_all_output(self):
        result = subprocess.run(
            ["bash", str(ROOT / "scripts/sdk-next-tag.sh")],
            env=self.env, capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "")

    def test_producer_errors_are_not_hidden(self):
        workflow = (ROOT / ".forgejo/workflows/ci.yml").read_text()
        expression = re.search(r'^\s*(latest_tag=".*")$', workflow, re.MULTILINE)
        env = dict(self.env, TAG_TEST_GIT_FAILURE="1")
        commands = [
            ["bash", "-euo", "pipefail", "-c", expression[1]],
            ["bash", str(ROOT / "scripts/sdk-next-tag.sh")],
        ]
        for command in commands:
            with self.subTest(command=command):
                result = subprocess.run(command, env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 42)


if __name__ == "__main__":
    unittest.main()
