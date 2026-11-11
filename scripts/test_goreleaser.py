#!/usr/bin/env python3
"""Exercise the real release config in disposable repos, without publishing.

Install the pinned GoReleaser with `mise install goreleaser` to run locally.
The GitHub release job installs it and runs these checks before publishing.
"""
import hashlib
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
GORELEASER = shutil.which('goreleaser')


@unittest.skipUnless(GORELEASER, 'install the pinned GoReleaser with mise install goreleaser')
class ReleaseConfigTest(unittest.TestCase):
    def run_release(self, tag, name, conflict):
        with tempfile.TemporaryDirectory(prefix='ocman-goreleaser-') as tmp:
            root = Path(tmp)
            shutil.copyfile(ROOT / '.goreleaser.yaml', root / '.goreleaser.yaml')
            (root / '.gitignore').write_text('dist/\n.release/\n')
            (root / 'go.mod').write_text('module example.com/releasefixture\n\ngo 1.23.0\n')
            (root / 'main.go').write_text(
                'package main\nimport "fmt"\nvar version string\n'
                'func main() { fmt.Println(version) }\n'
            )
            for filename in ('LICENSE', 'README.md'):
                (root / filename).write_text('Release fixture\n')
            release = root / '.release'
            release.mkdir()
            (release / 'ocman-darwin-arm64.dmg').write_bytes(b'dmg fixture')
            (release / 'changelog.md').write_text('Release fixture notes\n')
            env = dict(os.environ, GITHUB_TOKEN='', GH_TOKEN='', GITLAB_TOKEN='',
                       GITEA_TOKEN='', HOMEBREW_TAP_TOKEN='test-only',
                       GORELEASER_CURRENT_TAG=tag, GORELEASER_PREVIOUS_TAG='',
                       GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull)
            for args in (
                ('init', '--initial-branch=main'),
                ('config', 'user.name', 'Release fixture'),
                ('config', 'user.email', 'fixture@example.invalid'),
                ('remote', 'add', 'origin', 'https://github.com/ocman/ocman.git'),
                ('add', '.'), ('commit', '-m', 'release fixture'), ('tag', tag),
            ):
                subprocess.run(['git', *args], cwd=root, env=env, check=True,
                               capture_output=True, text=True)
            result = subprocess.run(
                [GORELEASER, 'release', '--clean', '--skip=publish',
                 '--release-notes=.release/changelog.md'],
                cwd=root, env=env, capture_output=True, text=True, timeout=180,
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertNotIn('deprecated', result.stdout + result.stderr)
            dist = root / 'dist'
            checksums = (dist / 'checksums.txt').read_text()
            cask = (dist / 'homebrew' / 'Casks' / f'{name}.rb').read_text()
            self.assertIn(f'cask "{name}" do', cask)
            self.assertIn(f'version "{tag[1:]}"', cask)
            self.assertIn(f'"{conflict}"', cask)
            self.assertIn('binary "ocman"', cask)
            self.assertRegex(cask, r'depends_on formula:\s*\[\s*"tmux"')
            self.assertIn('on_macos do', cask)
            self.assertIn('on_linux do', cask)
            self.assertEqual(cask.count('sha256 "'), 4)
            self.assertIn('/usr/bin/xattr', cask)
            self.assertIn('com.apple.quarantine', cask)
            self.assertEqual(len(list(dist.glob('ocman-*.tar.gz'))), 4)
            for os_name in ('linux', 'darwin'):
                for arch in ('amd64', 'arm64'):
                    filename = f'ocman-{os_name}-{arch}.tar.gz'
                    archive = dist / filename
                    checksum = hashlib.sha256(archive.read_bytes()).hexdigest()
                    self.assertIn(f'{checksum}  {filename}', checksums)
                    self.assertIn(checksum, cask)
                    self.assertIn(f'/{filename}', cask)
                    with tarfile.open(archive) as tar:
                        self.assertEqual(set(tar.getnames()), {'ocman', 'LICENSE', 'README.md'})
            # Extra release files are uploaded only in the skipped publish phase.
            self.assertIn('glob: .release/ocman-darwin-arm64.dmg',
                          (root / '.goreleaser.yaml').read_text())
            host_os = {'Darwin': 'darwin', 'Linux': 'linux'}.get(platform.system())
            host_arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64'}.get(platform.machine())
            if host_os and host_arch:
                with tarfile.open(dist / f'ocman-{host_os}-{host_arch}.tar.gz') as tar:
                    tar.extractall(root / 'smoke', filter='data')
                stamped = subprocess.check_output([str(root / 'smoke' / 'ocman')], text=True)
                self.assertEqual(stamped.strip(), tag)

    def test_stable_release(self):
        self.run_release('v1.2.3', 'ocman', 'ocman-nightly')

    def test_nightly_release(self):
        self.run_release('v1.3.0-nightly.20261112', 'ocman-nightly', 'ocman')


if __name__ == '__main__':
    unittest.main()
