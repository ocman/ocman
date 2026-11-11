#!/usr/bin/env python3
"""Exercise installation with fake network/toolchain commands and disposable paths."""
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
FAKE = r'''#!/usr/bin/env python3
import json, os, pathlib, shutil, sys
cmd = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ['CALLS'], 'a') as f:
    f.write(cmd + ' ' + ' '.join(args) + '\n')
if cmd == 'uname':
    print(os.environ.get('TEST_OS', 'Linux') if args == ['-s'] else os.environ.get('TEST_ARCH', 'x86_64'))
elif cmd == 'curl':
    url = next(a for a in args if a.startswith('https://'))
    if 'api.github.com' in url:
        if os.environ.get('API_FAIL'): sys.exit(22)
        if '/latest' in url: print(json.dumps({'tag_name': 'v1.2.3'}, indent=2))
        elif '&page=1' in url and os.environ.get('PAGINATE'):
            print(json.dumps([{'tag_name': 'v9.0.0-dev.1'}], indent=2))
        elif '&page=1' in url or ('&page=2' in url and os.environ.get('PAGINATE')):
            print(json.dumps([{'tag_name': 'v9.0.0-dev.1'}, {'tag_name': 'v1.3.0-nightly.20261108'}], indent=2))
        else: print('[]')
    elif '/download/' in url:
        if os.environ.get('DOWNLOAD_FAIL'): sys.exit(22)
        shutil.copyfile(os.environ['ARCHIVE'], args[args.index('-o') + 1])
    else:
        shutil.copyfile(os.environ['INSTALLER'], args[args.index('-o') + 1])
elif cmd == 'git':
    if args[0] == 'ls-remote':
        print('abc\trefs/tags/v1.2.3\nabc\trefs/tags/v1.3.0-nightly.20261108\nabc\trefs/tags/v10.0.0-dev.1')
    elif args[0] == 'clone':
        p = pathlib.Path(args[-1])
        (p / 'frontend').mkdir(parents=True)
        (p / '.git').mkdir()
    elif 'status' in args and os.environ.get('DIRTY'): print(' M user-file')
elif cmd == 'go':
    if os.environ.get('BUILD_FAIL'): sys.exit(1)
    pathlib.Path(args[args.index('-o') + 1]).write_text('built binary')
'''


class InstallTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='ocman-install-')
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        tools = self.base / 'tools'
        tools.mkdir()
        for name in ('curl', 'git', 'go', 'pnpm', 'node', 'uname'):
            tool = tools / name
            tool.write_text(FAKE)
            tool.chmod(0o755)
        archive = self.base / 'binary.tar.gz'
        with tarfile.open(archive, 'w:gz') as tar:
            for os_name in ('linux', 'darwin'):
                for arch in ('amd64', 'arm64'):
                    member = tarfile.TarInfo(f'ocman-{os_name}-{arch}')
                    member.size = len(b'release binary')
                    tar.addfile(member, io.BytesIO(b'release binary'))
        self.prefix = self.base / 'prefix'
        self.env = {
            **os.environ, 'PATH': f'{tools}:{os.environ["PATH"]}',
            'OCMAN_PREFIX': str(self.prefix), 'OCMAN_RUN': str(self.base / 'run'),
            'CALLS': str(self.base / 'calls'), 'ARCHIVE': str(archive),
            'INSTALLER': str(ROOT / 'install.sh'), 'TMPDIR': str(self.base),
        }
        for key in ('OCMAN_VERSION', 'OCMAN_SRC', 'OCMAN_BIN', 'OCMAN_REPO'):
            self.env.pop(key, None)

    def run_install(self, *args, success=True, piped=False):
        command = ['bash', '-s', '--', *args] if piped else ['bash', str(ROOT / 'install.sh'), *args]
        result = subprocess.run(command, env=self.env, text=True, capture_output=True, timeout=20,
                                input=(ROOT / 'install.sh').read_text() if piped else None)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        return result

    def calls(self):
        return (self.base / 'calls').read_text()

    def test_stable_binary_without_build(self):
        self.run_install('install', '--no-start')
        self.assertEqual((self.prefix / 'bin/ocman').read_text(), 'release binary')
        self.assertIn('/v1.2.3/ocman-linux-amd64.tar.gz', self.calls())
        self.assertNotIn('git ', self.calls())
        self.assertNotIn('go ', self.calls())
        self.assertTrue(os.access(self.prefix / 'bin/ocman-ctl', os.X_OK))
        self.assertFalse((self.base / 'run').exists())

    def test_goreleaser_archive_installs_without_build(self):
        with tarfile.open(self.env['ARCHIVE'], 'w:gz') as tar:
            member = tarfile.TarInfo('ocman')
            member.size = len(b'release binary')
            tar.addfile(member, io.BytesIO(b'release binary'))
        self.run_install('install', '--no-start')
        self.assertEqual((self.prefix / 'bin/ocman').read_text(), 'release binary')
        self.assertNotIn('go ', self.calls())

    def test_nightly_and_saved_update(self):
        self.run_install('install', 'nightly', '--no-start')
        self.run_install('update', '--no-start')
        self.assertEqual(self.calls().count('/v1.3.0-nightly.20261108/ocman-linux-amd64.tar.gz'), 2)

    def test_exact_version_and_macos_arm(self):
        self.env.update(TEST_OS='Darwin', TEST_ARCH='aarch64')
        self.run_install('install', '1.2.0', '--no-start')
        self.assertIn('/v1.2.0/ocman-darwin-arm64.tar.gz', self.calls())
        self.assertNotIn('api.github.com', self.calls())

    def test_missing_binary_builds_exact_tag(self):
        self.env['DOWNLOAD_FAIL'] = '1'
        self.run_install('install', 'v1.2.0-dev.3', '--no-start')
        self.assertIn('--branch v1.2.0-dev.3', self.calls())
        self.assertIn('pnpm install --frozen-lockfile', self.calls())
        self.assertIn('-X main.version=v1.2.0-dev.3', self.calls())
        self.assertEqual((self.prefix / 'bin/ocman').read_text(), 'built binary')

    def test_source_mode_and_api_failure(self):
        self.env['API_FAIL'] = '1'
        self.run_install('install', 'stable', '--source', '--no-start')
        self.assertIn('ls-remote --tags --refs', self.calls())
        self.assertIn('--branch v1.2.3', self.calls())
        self.assertNotIn('/download/', self.calls())

    def test_failed_build_keeps_installed_binary(self):
        self.run_install('install', '--no-start')
        self.env['BUILD_FAIL'] = '1'
        self.run_install('update', '--source', '--no-start', success=False)
        self.assertEqual((self.prefix / 'bin/ocman').read_text(), 'release binary')

    def test_dirty_source_is_preserved(self):
        self.run_install('install', '--source', '--no-start')
        self.env['DIRTY'] = '1'
        self.run_install('update', '--source', '--no-start', success=False)
        self.assertNotIn('reset --hard', self.calls())

    def test_invalid_version(self):
        self.run_install('install', '../main', '--no-start', success=False)
        self.assertNotIn('curl ', self.calls())

    def test_pipe_install(self):
        self.run_install('install', '--no-start', piped=True)
        self.assertEqual((self.prefix / 'bin/ocman-ctl').read_text(), (ROOT / 'install.sh').read_text())

    def test_controller_refresh_requires_bootstrap(self):
        self.run_install('install', '--no-start')
        controller = self.prefix / 'bin/ocman-ctl'
        old_controller = controller.read_text() + '\n# installed controller marker\n'
        controller.write_text(old_controller)
        result = subprocess.run(['bash', str(controller), 'update', '--no-start'],
                                env=self.env, text=True, capture_output=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(controller.read_text(), old_controller)
        self.run_install('install', '--no-start', piped=True)
        self.assertEqual(controller.read_text(), (ROOT / 'install.sh').read_text())

    def test_nightly_pagination(self):
        self.env['PAGINATE'] = '1'
        self.run_install('install', 'nightly', '--no-start')
        self.assertIn('per_page=100&page=2', self.calls())

    def test_corrupt_archive_falls_back(self):
        Path(self.env['ARCHIVE']).write_text('not a tar archive')
        self.run_install('install', 'v1.2.3', '--no-start')
        self.assertEqual((self.prefix / 'bin/ocman').read_text(), 'built binary')

    def test_unsupported_architecture_builds_locally(self):
        self.env['TEST_ARCH'] = 'riscv64'
        self.run_install('install', 'v1.2.3', '--no-start')
        self.assertIn('--branch v1.2.3', self.calls())
        self.assertNotIn('/download/', self.calls())

    def test_unknown_option_fails_before_install(self):
        self.run_install('install', '--invalid', success=False)
        self.assertFalse((self.prefix / 'bin/ocman').exists())


if __name__ == '__main__':
    unittest.main()
