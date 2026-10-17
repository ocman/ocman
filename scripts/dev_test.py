"""Exercise dev log-reader lifetime without starting the application or databases."""

import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time
import unittest


class DevLogsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "scripts").mkdir()
        (self.root / "frontend").mkdir()
        (self.root / "bin").mkdir()
        shutil.copy(Path(__file__).with_name("dev.sh"), self.root / "scripts/dev.sh")
        self.env = dict(os.environ, PATH=f'{self.root / "bin"}:{os.environ["PATH"]}',
                        DEV_TEST_ROOT=str(self.root))
        self.processes = []
        self.addCleanup(self.stop_processes)
        for name in ("air", "pnpm"):
            self.command(name, '''#!/usr/bin/env python3
import os, time
from pathlib import Path
Path(os.environ["DEV_TEST_ROOT"], os.path.basename(__file__) + ".pid").write_text(str(os.getpid()))
print("DEV_TEST_" + os.path.basename(__file__), flush=True)
while True:
    time.sleep(1)
''')
        self.command("lsof", "#!/bin/sh\nexit 0\n")
        self.command("tail", f'''#!/bin/sh
echo $$ > "$DEV_TEST_ROOT/tail.pid"
exec {shutil.which("tail")} "$@"
''')

    def command(self, name, text):
        path = self.root / "bin" / name
        path.write_text(text)
        path.chmod(0o755)

    def stop_processes(self):
        for process in self.processes:
            if process.poll() is None:
                process.kill()
            process.wait()
        for path in self.root.glob("*.pid"):
            try:
                os.kill(int(path.read_text()), signal.SIGKILL)
            except ProcessLookupError:
                pass

    def wait_for(self, condition, message):
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            if condition():
                return
            time.sleep(0.05)
        self.fail(message)

    def start(self):
        output = open(self.root / "output", "ab", buffering=0)
        self.addCleanup(output.close)
        process = subprocess.Popen(
            ["bash", "scripts/dev.sh", "prod"], cwd=self.root, env=self.env,
            stdin=subprocess.DEVNULL, stdout=output, stderr=output,
            start_new_session=True,
        )
        self.processes.append(process)
        self.wait_for(lambda: (self.root / "tail.pid").exists(), "tail never started")
        self.wait_for(lambda: b"DEV_TEST_air" in (self.root / "output").read_bytes(),
                      "backend log never reached terminal")
        return process, int((self.root / "tail.pid").read_text())

    def alive(self, pid):
        result = subprocess.run(["ps", "-o", "stat=", "-p", str(pid)],
                                capture_output=True, text=True, check=False)
        return bool(result.stdout.strip()) and not result.stdout.strip().startswith("Z")

    def test_forced_stop_reaps_log_reader(self):
        process, tail = self.start()
        process.kill()
        process.wait()
        self.wait_for(lambda: not self.alive(tail),
                      "orphan log reader survives and duplicates the next run's output")

    def test_normal_stop_reaps_log_reader(self):
        process, tail = self.start()
        process.terminate()
        process.wait(timeout=8)
        self.wait_for(lambda: not self.alive(tail), "log reader survived normal shutdown")

    def test_logs_are_printed_once(self):
        process, _ = self.start()
        self.wait_for(lambda: b"DEV_TEST_pnpm" in (self.root / "output").read_bytes(),
                      "frontend log never reached terminal")
        process.terminate()
        process.wait(timeout=8)
        output = (self.root / "output").read_bytes()
        self.assertEqual(output.count(b"DEV_TEST_air"), 1)
        self.assertEqual(output.count(b"DEV_TEST_pnpm"), 1)


if __name__ == "__main__":
    unittest.main()
