#!/usr/bin/env python3
"""Fail unless TAG is a published GitHub release of commit SHA with every asset.

    check_published_release.py TAG SHA

Cut Release runs this before promoting a nightly to stable, so a stable
release only ships a build that nightly users could download. Any lookup
error fails closed.

Environment:
    GITHUB_API_URL        API base (default https://api.github.com)
    RELEASE_MIRROR_REPO   owner/name of the GitHub mirror (default ocman/ocman)
    GITHUB_MIRROR_TOKEN   optional token, to avoid the anonymous rate limit
"""
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

# Keep in step with the assets .github/workflows/release.yml publishes.
REQUIRED_ASSETS = (
    "ocman-linux-amd64.tar.gz",
    "ocman-linux-arm64.tar.gz",
    "ocman-darwin-amd64.tar.gz",
    "ocman-darwin-arm64.tar.gz",
    "ocman-darwin-arm64.dmg",
)


class NotPublished(Exception):
    pass


def get(path):
    base = os.environ.get("GITHUB_API_URL", "https://api.github.com").rstrip("/")
    request = urllib.request.Request(base + path, headers={
        "Accept": "application/vnd.github+json",
        "User-Agent": "ocman-release",
    })
    token = os.environ.get("GITHUB_MIRROR_TOKEN")
    if token:
        request.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        raise NotPublished(f"GET {path} returned HTTP {error.code}") from None
    except (urllib.error.URLError, TimeoutError, ValueError) as error:
        raise NotPublished(f"GET {path} failed: {error}") from None


def check(tag, sha):
    repo = os.environ.get("RELEASE_MIRROR_REPO", "ocman/ocman")
    quoted = urllib.parse.quote(tag, safe="")

    release = get(f"/repos/{repo}/releases/tags/{quoted}")
    if release.get("draft") is not False:
        raise NotPublished(f"{tag} is a draft or has no draft state")
    uploaded = {
        asset.get("name") for asset in release.get("assets") or []
        if asset.get("state") == "uploaded"
    }
    missing = [name for name in REQUIRED_ASSETS if name not in uploaded]
    if missing:
        raise NotPublished(f"{tag} is missing assets: {', '.join(missing)}")

    commit = get(f"/repos/{repo}/commits/{quoted}")
    if commit.get("sha") != sha:
        raise NotPublished(f"{tag} resolves to {commit.get('sha')} on GitHub, not {sha}")


def main(argv):
    if len(argv) != 3:
        print("usage: check_published_release.py TAG SHA", file=sys.stderr)
        return 2
    try:
        check(argv[1], argv[2])
    except NotPublished as error:
        print(f"check_published_release: {error}", file=sys.stderr)
        return 1
    print(f"{argv[1]} is published for {argv[2]}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
