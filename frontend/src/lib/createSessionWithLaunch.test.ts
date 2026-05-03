import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createSessionWithLaunch, type LaunchStatus } from './createSessionWithLaunch';

function unreachable(): Error & { code: string } {
  const err = new Error('no running platform instance') as Error & { code: string };
  err.code = 'unreachable';
  return err;
}

describe('createSessionWithLaunch', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('returns the session id when createSession succeeds on the first try', async () => {
    const createSession = vi.fn().mockResolvedValue({ id: 'abc' });
    const launchOpencodeInTmux = vi.fn();

    const res = await createSessionWithLaunch(
      { createSession, launchOpencodeInTmux, tmuxAvailable: true },
      { directory: '/tmp/foo' },
    );

    expect(res).toEqual({ id: 'abc' });
    expect(createSession).toHaveBeenCalledTimes(1);
    expect(launchOpencodeInTmux).not.toHaveBeenCalled();
  });

  it('rethrows non-unreachable errors without launching', async () => {
    const err = new Error('boom');
    const createSession = vi.fn().mockRejectedValue(err);
    const launchOpencodeInTmux = vi.fn();

    await expect(
      createSessionWithLaunch(
        { createSession, launchOpencodeInTmux, tmuxAvailable: true },
        { directory: '/tmp/foo' },
      ),
    ).rejects.toBe(err);
    expect(launchOpencodeInTmux).not.toHaveBeenCalled();
  });

  it('rethrows unreachable errors when tmux is unavailable', async () => {
    const err = unreachable();
    const createSession = vi.fn().mockRejectedValue(err);
    const launchOpencodeInTmux = vi.fn();

    await expect(
      createSessionWithLaunch(
        { createSession, launchOpencodeInTmux, tmuxAvailable: false },
        { directory: '/tmp/foo' },
      ),
    ).rejects.toBe(err);
    expect(launchOpencodeInTmux).not.toHaveBeenCalled();
  });

  it('launches opencode and retries, reporting status transitions', async () => {
    const createSession = vi
      .fn()
      .mockRejectedValueOnce(unreachable())
      .mockResolvedValueOnce({ id: 'new-id' });
    const launchOpencodeInTmux = vi.fn().mockResolvedValue({ session: 'foo' });
    const statuses: LaunchStatus[] = [];

    const promise = createSessionWithLaunch(
      {
        createSession,
        launchOpencodeInTmux,
        tmuxAvailable: true,
        onStatusChange: (s) => statuses.push(s),
      },
      { directory: '/tmp/foo' },
    );

    // Drain the async work: first createSession rejects, then launch
    // runs, then we wait for the retry delay before the retry call.
    await vi.runAllTimersAsync();
    const res = await promise;

    expect(res).toEqual({ id: 'new-id' });
    expect(launchOpencodeInTmux).toHaveBeenCalledWith('/tmp/foo');
    expect(createSession).toHaveBeenCalledTimes(2);
    expect(statuses).toEqual(['launching', 'retrying', 'idle']);
  });

  it('rethrows the original error when launch itself fails', async () => {
    const originalErr = unreachable();
    const createSession = vi.fn().mockRejectedValue(originalErr);
    const launchOpencodeInTmux = vi.fn().mockRejectedValue(new Error('tmux broken'));

    await expect(
      createSessionWithLaunch(
        { createSession, launchOpencodeInTmux, tmuxAvailable: true },
        { directory: '/tmp/foo' },
      ),
    ).rejects.toBe(originalErr);
    expect(createSession).toHaveBeenCalledTimes(1);
  });

  it('retries without launching when alreadyLaunched=true and tmux is unavailable', async () => {
    // /wt path: the worktree backend has already started opencode in
    // a tmux window; we must not call launchOpencodeInTmux a second
    // time, but we must keep retrying until the lsof scan picks up
    // the new instance and createSession succeeds.
    const createSession = vi
      .fn()
      .mockRejectedValueOnce(unreachable())
      .mockRejectedValueOnce(unreachable())
      .mockResolvedValueOnce({ id: 'wt-session' });
    const launchOpencodeInTmux = vi.fn();

    const promise = createSessionWithLaunch(
      { createSession, launchOpencodeInTmux, tmuxAvailable: false },
      { directory: '/tmp/wt', alreadyLaunched: true },
    );
    await vi.runAllTimersAsync();
    const res = await promise;

    expect(res).toEqual({ id: 'wt-session' });
    expect(launchOpencodeInTmux).not.toHaveBeenCalled();
    expect(createSession).toHaveBeenCalledTimes(3);
  });

  it('gives up retrying if the retry returns a non-unreachable error', async () => {
    const fatal = new Error('auth required');
    const createSession = vi
      .fn()
      .mockRejectedValueOnce(unreachable())
      .mockRejectedValueOnce(fatal);
    const launchOpencodeInTmux = vi.fn().mockResolvedValue({ session: 'foo' });

    const promise = createSessionWithLaunch(
      { createSession, launchOpencodeInTmux, tmuxAvailable: true },
      { directory: '/tmp/foo' },
    );
    // Attach the assertion before advancing fake timers so the
    // rejection doesn't surface as unhandled while awaiting timers.
    const assertion = expect(promise).rejects.toBe(fatal);
    await vi.runAllTimersAsync();
    await assertion;
    // Only one retry attempt after launch.
    expect(createSession).toHaveBeenCalledTimes(2);
  });
});
