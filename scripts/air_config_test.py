"""Check the Air launch contract without starting ocman or touching a database."""
from pathlib import Path
import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import tomllib
import unittest


class AirConfigTest(unittest.TestCase):
    def test_entrypoint_preserves_arguments_and_graceful_shutdown(self):
        config = tomllib.loads((Path(__file__).resolve().parent.parent / '.air.toml').read_text())
        build = config['build']
        self.assertNotIn('bin', build, 'build.bin emits a deprecation warning')
        self.assertNotIn('full_bin', build, 'full_bin overrides the entrypoint')
        self.assertEqual(build['entrypoint'], ['./tmp/ocman', '-addr', '127.0.0.1:8229'])
        self.assertEqual(build['cmd'], 'go build -o ./tmp/ocman .')
        self.assertTrue(build['send_interrupt'])
        self.assertEqual(build['kill_delay'], '2s')
        self.assertFalse(config['misc']['clean_on_exit'])

    @unittest.skipUnless(shutil.which('air'), 'Air is not installed')
    def test_air_runs_only_a_fixture_with_the_expected_args_and_sigint(self):
        self.run_air_fixture([], ['-addr', '127.0.0.1:8229'])

    @unittest.skipUnless(shutil.which('air'), 'Air is not installed')
    def test_remote_full_bin_override_keeps_its_arguments(self):
        self.run_air_fixture([
            '--build.full_bin', './tmp/ocman -addr 127.0.0.1:8229 -remote-listen 0.0.0.0:8230',
        ], ['-addr', '127.0.0.1:8229', '-remote-listen', '0.0.0.0:8230'])

    def run_air_fixture(self, args, expected):
        # Exercise the installed Air, never the real build command or binary.
        source = (Path(__file__).resolve().parent.parent / '.air.toml').read_text()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'tmp').mkdir()
            (root / '.air.toml').write_text(source.replace(
                'cmd = "go build -o ./tmp/ocman ."', 'cmd = "true"'))
            binary = root / 'tmp/ocman'
            binary.write_text(f'#!{sys.executable}\n' + '''import json, signal, sys, time
from pathlib import Path
def stop(*args):
    Path('interrupted').write_text('SIGINT')
    sys.exit(0)
signal.signal(signal.SIGINT, stop)
Path('args.json').write_text(json.dumps(sys.argv[1:]))
while True:
    time.sleep(0.1)
''')
            binary.chmod(0o755)
            with (root / 'output').open('w+') as output:
                process = subprocess.Popen(['air', '-c', '.air.toml', *args], cwd=root,
                                           stdout=output, stderr=output, start_new_session=True)
                try:
                    deadline = time.monotonic() + 15
                    while not (root / 'args.json').exists() and time.monotonic() < deadline:
                        time.sleep(0.05)
                    self.assertTrue((root / 'args.json').exists(), 'Air did not start the fixture')
                    self.assertEqual(json.loads((root / 'args.json').read_text()), expected)
                    process.send_signal(signal.SIGINT)
                    process.wait(timeout=10)
                    self.assertEqual((root / 'interrupted').read_text(), 'SIGINT')
                    output.seek(0)
                    self.assertNotIn('build.bin is deprecated', output.read())
                finally:
                    # Only this fixture's private group, including any child Air left.
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    process.wait()


if __name__ == '__main__':
    unittest.main()
