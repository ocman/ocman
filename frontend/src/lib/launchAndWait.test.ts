import { describe, it, expect, vi } from 'vitest';
import { launchAndWait } from './launchAndWait';
import type { LaunchProgressReporter } from './launchProgressStore';

function fakeProgress() {
  const calls: string[] = [];
  const p: LaunchProgressReporter = {
    begin: () => calls.push('begin'),
    step: (s) => calls.push(`step:${s}`),
    attempt: (a, m) => calls.push(`attempt:${a}/${m}`),
    succeed: () => calls.push('succeed'),
    fail: (msg) => calls.push(`fail:${msg}`),
  };
  return { p, calls };
}

const noWait = () => Promise.resolve();

describe('launchAndWait', () => {
  it('succeeds once the session becomes live', async () => {
    const { p, calls } = fakeProgress();
    const launch = vi.fn().mockResolvedValue(undefined);
    const reload = vi.fn().mockResolvedValue(undefined);
    let live = false;
    // Become reachable on the 2nd poll.
    reload.mockImplementation(async () => {
      if (reload.mock.calls.length >= 2) live = true;
    });

    await launchAndWait('/repo', {
      launch,
      reload,
      isLive: () => live,
      progress: p,
      wait: noWait,
    });

    expect(launch).toHaveBeenCalledWith('/repo');
    expect(calls).toContain('succeed');
    expect(calls).not.toContain('fail:');
    expect(reload).toHaveBeenCalledTimes(2);
  });

  it('fails and rethrows when the launch itself errors', async () => {
    const { p, calls } = fakeProgress();
    const launch = vi.fn().mockRejectedValue(new Error('tmux boom'));
    const reload = vi.fn();

    await expect(
      launchAndWait('/repo', { launch, reload, isLive: () => false, progress: p, wait: noWait }),
    ).rejects.toThrow('tmux boom');

    expect(reload).not.toHaveBeenCalled();
    expect(calls.some((c) => c.startsWith('fail:'))).toBe(true);
    expect(calls).not.toContain('succeed');
  });

  it('fails when the instance never becomes reachable', async () => {
    const { p, calls } = fakeProgress();
    const launch = vi.fn().mockResolvedValue(undefined);
    const reload = vi.fn().mockResolvedValue(undefined);

    await expect(
      launchAndWait('/repo', { launch, reload, isLive: () => false, progress: p, wait: noWait }),
    ).rejects.toThrow(/did not become reachable/);

    // Polled the full retry budget (5 attempts).
    expect(reload).toHaveBeenCalledTimes(5);
    expect(calls.some((c) => c.startsWith('fail:'))).toBe(true);
  });
});
