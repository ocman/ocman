// Older Node runtimes reject this flag before a Vitest worker can start.
export function workerExecArgv(supportedFlags: ReadonlySet<string>): string[] {
  const flag = '--no-experimental-webstorage';
  return supportedFlags.has(flag) ? [flag] : [];
}
