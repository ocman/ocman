#!/usr/bin/env python3
"""Exercise check_published_release.py against a fake GitHub API."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import sys
import threading
import unittest

SCRIPT = Path(__file__).resolve().parent / "check_published_release.py"
TAG = "v0.2.0-nightly.20261107"
SHA = "a" * 40
ASSETS = [
    "ocman-linux-amd64.tar.gz",
    "ocman-linux-arm64.tar.gz",
    "ocman-darwin-amd64.tar.gz",
    "ocman-darwin-arm64.tar.gz",
    "ocman-darwin-arm64.dmg",
]


def published(**overrides):
    release = {
        "draft": False,
        "prerelease": True,
        "assets": [{"name": name, "state": "uploaded"} for name in ASSETS],
    }
    release.update(overrides)
    return release


class FakeGitHub(BaseHTTPRequestHandler):
    routes = {}
    seen_auth = []

    def do_GET(self):
        FakeGitHub.seen_auth.append(self.headers.get("Authorization"))
        status, body = FakeGitHub.routes.get(self.path, (404, {"message": "Not Found"}))
        payload = body if isinstance(body, bytes) else json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *args):
        pass


class CheckPublishedReleaseTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), FakeGitHub)
        threading.Thread(target=cls.server.serve_forever, daemon=True).start()
        cls.api = f"http://127.0.0.1:{cls.server.server_address[1]}"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()

    def setUp(self):
        FakeGitHub.seen_auth = []
        self.serve(release=published(), commit=(200, {"sha": SHA}))

    def serve(self, release, commit):
        base = "/repos/ocman/ocman"
        FakeGitHub.routes = {
            f"{base}/releases/tags/{TAG}": release if isinstance(release, tuple) else (200, release),
            f"{base}/commits/{TAG}": commit,
        }

    def run_check(self, *args, api=None, token=None):
        env = {k: v for k, v in os.environ.items() if k != "GITHUB_MIRROR_TOKEN"}
        env["GITHUB_API_URL"] = api or self.api
        if token:
            env["GITHUB_MIRROR_TOKEN"] = token
        return subprocess.run(
            [sys.executable, "-I", str(SCRIPT), *(args or (TAG, SHA))],
            env=env, capture_output=True, text=True,
        )

    def assert_rejected(self, fragment, **kwargs):
        result = self.run_check(**kwargs)
        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn(fragment, result.stderr)

    def test_published_nightly_passes(self):
        result = self.run_check(token="secret")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(FakeGitHub.seen_auth, ["Bearer secret", "Bearer secret"])

    def test_missing_release_fails(self):
        self.serve(release=(404, {"message": "Not Found"}), commit=(200, {"sha": SHA}))
        self.assert_rejected("HTTP 404")

    def test_draft_release_fails(self):
        self.serve(release=published(draft=True), commit=(200, {"sha": SHA}))
        self.assert_rejected("draft")

    def test_pending_or_failed_packaging_fails(self):
        assets = [{"name": name, "state": "uploaded"} for name in ASSETS[:-1]]
        assets.append({"name": ASSETS[-1], "state": "starter"})
        self.serve(release=published(assets=assets), commit=(200, {"sha": SHA}))
        self.assert_rejected("missing assets: ocman-darwin-arm64.dmg")

    def test_tag_on_another_commit_fails(self):
        self.serve(release=published(), commit=(200, {"sha": "b" * 40}))
        self.assert_rejected("not " + SHA)

    def test_lookup_errors_fail_closed(self):
        self.serve(release=(500, {"message": "boom"}), commit=(200, {"sha": SHA}))
        self.assert_rejected("HTTP 500")
        self.serve(release=(200, b"not json"), commit=(200, {"sha": SHA}))
        self.assert_rejected("failed")
        self.assert_rejected("failed", api="http://127.0.0.1:9")

    def test_usage(self):
        result = self.run_check(TAG)
        self.assertEqual(result.returncode, 2)


if __name__ == "__main__":
    unittest.main()
