import { Worker } from 'node:worker_threads';
import { expect, it } from 'vitest';
import { workerExecArgv } from './workerExecArgv';

it('does not pass the webstorage flag on Node versions that do not support it', () => {
  expect(workerExecArgv(new Set())).toEqual([]);
});

it('disables native webstorage when Node supports the flag', () => {
  expect(workerExecArgv(new Set(['--no-experimental-webstorage']))).toEqual(['--no-experimental-webstorage']);
});

it('starts a worker with the flags supported by the current Node runtime', async () => {
  const worker = new Worker('', { eval: true, execArgv: workerExecArgv(process.allowedNodeEnvironmentFlags) });
  const exitCode = await new Promise<number>((resolve, reject) => {
    worker.once('error', reject);
    worker.once('exit', resolve);
  });
  expect(exitCode).toBe(0);
});
