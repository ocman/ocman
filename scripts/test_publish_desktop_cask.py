#!/usr/bin/env python3
"""Exercise publish_desktop_cask.py against a fake GitHub contents API."""
import base64
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest

SCRIPT = Path(__file__).resolve().parent / "publish_desktop_cask.py"
STABLE = "v1.2.3"
NIGHTLY = "v1.3.0-nightly.20261112"
DMG = b"dmg fixture"
DMG_SHA = hashlib.sha256(DMG).hexdigest()


def cask_path(name):
    return f"/repos/ocman/homebrew-tap/contents/Casks/{name}.rb"


class FakeGitHub(BaseHTTPRequestHandler):
    files = {}
    puts = []
    put_status = 200
    seen_auth = []

    def do_GET(self):
        FakeGitHub.seen_auth.append(self.headers.get("Authorization"))
        path, _, query = self.path.partition("?")
        if query != "ref=main" or path not in FakeGitHub.files:
            return self.reply(404, {"message": "Not Found"})
        content = FakeGitHub.files[path]
        self.reply(200, {"sha": "old-sha", "content": base64.b64encode(content.encode()).decode()})

    def do_PUT(self):
        FakeGitHub.seen_auth.append(self.headers.get("Authorization"))
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        FakeGitHub.puts.append((self.path, body))
        self.reply(FakeGitHub.put_status, {"content": {}})

    def reply(self, status, body):
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(body).encode())

    def log_message(self, *args):
        pass


class PublishDesktopCaskTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), FakeGitHub)
        threading.Thread(target=cls.server.serve_forever, daemon=True).start()
        cls.api = f"http://127.0.0.1:{cls.server.server_address[1]}"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()

    def setUp(self):
        FakeGitHub.files, FakeGitHub.puts, FakeGitHub.seen_auth = {}, [], []
        FakeGitHub.put_status = 200
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.dmg = Path(tmp.name) / "ocman-darwin-arm64.dmg"
        self.dmg.write_bytes(DMG)

    def run_script(self, *args, token="secret"):
        env = {k: v for k, v in os.environ.items() if k != "HOMEBREW_TAP_TOKEN"}
        env["GITHUB_API_URL"] = self.api
        if token:
            env["HOMEBREW_TAP_TOKEN"] = token
        return subprocess.run(
            [sys.executable, "-I", str(SCRIPT), *args],
            env=env, capture_output=True, text=True,
        )

    def render(self, tag):
        result = self.run_script(tag, str(self.dmg), "--print", token=None)
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout

    def test_stable_cask(self):
        cask = self.render(STABLE)
        self.assertIn('cask "ocman-desktop" do', cask)
        self.assertIn('version "1.2.3"', cask)
        self.assertIn('conflicts_with cask: "ocman-desktop-nightly"', cask)
        self.assertIn(f'sha256 "{DMG_SHA}"', cask)
        self.assertIn('/download/v#{version}/ocman-darwin-arm64.dmg"', cask)
        self.assertIn('app "ocman.app"', cask)
        self.assertIn("depends_on arch: :arm64", cask)
        self.assertIn('depends_on formula: "tmux"', cask)
        self.assertIn('"-dr", "com.apple.quarantine", "{{appdir}}/ocman.app"', cask)
        self.assertIn("caveats <<~EOS", cask)
        for package in ("brew install opencode", "brew install --cask claude-code",
                        "brew install --cask codex"):
            self.assertIn(package, cask)
        self.assertEqual(FakeGitHub.puts, [])

    def test_nightly_cask(self):
        cask = self.render(NIGHTLY)
        self.assertIn('cask "ocman-desktop-nightly" do', cask)
        self.assertIn('version "1.3.0-nightly.20261112"', cask)
        self.assertIn('conflicts_with cask: "ocman-desktop"', cask)

    def test_creates_missing_cask(self):
        result = self.run_script(NIGHTLY, str(self.dmg))
        self.assertEqual(result.returncode, 0, result.stderr)
        [(path, body)] = FakeGitHub.puts
        self.assertEqual(path, cask_path("ocman-desktop-nightly"))
        self.assertEqual(body["branch"], "main")
        self.assertNotIn("sha", body)
        self.assertEqual(body["message"], f"chore(homebrew): update ocman-desktop-nightly to {NIGHTLY}")
        self.assertEqual(base64.b64decode(body["content"]).decode(), self.render(NIGHTLY))
        self.assertEqual(FakeGitHub.seen_auth, ["Bearer secret", "Bearer secret"])

    def test_updates_existing_cask(self):
        FakeGitHub.files[cask_path("ocman-desktop")] = "old cask\n"
        result = self.run_script(STABLE, str(self.dmg))
        self.assertEqual(result.returncode, 0, result.stderr)
        [(path, body)] = FakeGitHub.puts
        self.assertEqual(path, cask_path("ocman-desktop"))
        self.assertEqual(body["sha"], "old-sha")

    def test_unchanged_cask_is_not_committed(self):
        FakeGitHub.files[cask_path("ocman-desktop")] = self.render(STABLE)
        result = self.run_script(STABLE, str(self.dmg))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(FakeGitHub.puts, [])

    def test_failed_commit_fails(self):
        FakeGitHub.put_status = 409
        result = self.run_script(STABLE, str(self.dmg))
        self.assertEqual(result.returncode, 1)
        self.assertIn("HTTP 409", result.stderr)

    def test_missing_token_fails(self):
        result = self.run_script(STABLE, str(self.dmg), token=None)
        self.assertEqual(result.returncode, 1)
        self.assertIn("HOMEBREW_TAP_TOKEN", result.stderr)

    def test_rejects_dev_tag_and_missing_dmg(self):
        result = self.run_script("v1.2.3-dev.4", str(self.dmg), "--print")
        self.assertEqual(result.returncode, 1)
        self.assertIn("not a nightly or stable", result.stderr)
        result = self.run_script(STABLE, str(self.dmg) + ".missing", "--print")
        self.assertEqual(result.returncode, 1)

    def test_usage(self):
        self.assertEqual(self.run_script(STABLE).returncode, 2)


if __name__ == "__main__":
    unittest.main()
