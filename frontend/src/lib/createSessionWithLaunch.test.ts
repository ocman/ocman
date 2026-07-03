import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createSessionWithLaunch } from './createSessionWithLaunch';
import { useLaunchProgressStore } from './launchProgressStore';

function unreachable(): Error & { code: string } {
  const err = new Error('no running platform instance') as Error & { code: string };
  err.code = 'unreachable';
  return err;
}

/**
 * Records deduplicated `phase:step:attempt` transitions from the
 * global launch-progress store. Zustand notifies on every setState
 * call (even no-op merges), so consecutive identical signatures are
 * collapsed to keep assertions stable.
 */
function trackProgress() {
  const events: string[] = [];
  let last = '';
  const unsub = useLaunchProgressStore.subscribe((s) => {
    const sig = `${s.phase}:${s.step}:${s.attempt}`;
    if (sig !== last) {
      last = sig;
      events.push(sig);
    }
  });
  return { events, unsub };
}

describe('createSessionWithLaunch', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    useLaunchProgressStore.setState({
      phase: 'idle',
      directory: '',
      step: 'launch',
      attempt: 0,
      maxAttempts: 0,
      skipLaunch: false,
      error: null,
    });
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
    // Fast path: the progress overlay never appears.
    expect(useLaunchProgressStore.getState().phase).toBe('idle');
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
    expect(useLaunchProgressStore.getState().phase).toBe('idle');
  });

  it('rethrows unreachable errors when tmux is unavailable, surfacing an error in the overlay', async () => {
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
    const state = useLaunchProgressStore.getState();
    expect(state.phase).toBe('error');
    expect(state.error).toMatch(/tmux is not on its PATH/);
  });

  it('launches opencode and retries, reporting step-by-step progress', async () => {
    const createSession = vi
      .fn()
      .mockRejectedValueOnce(unreachable())
      .mockResolvedValueOnce({ id: 'new-id' });
    const launchOpencodeInTmux = vi.fn().mockResolvedValue({ session: 'foo' });
    const { events, unsub } = trackProgress();

    const promise = createSessionWithLaunch(
      { createSession, launchOpencodeInTmux, tmuxAvailable: true },
      { directory: '/tmp/foo' },
    );

    // Drain the async work: first createSession rejects, then launch
    // runs, then we wait for the retry delay before the retry call.
    await vi.runAllTimersAsync();
    const res = await promise;
    unsub();

    expect(res).toEqual({ id: 'new-id' });
    expect(launchOpencodeInTmux).toHaveBeenCalledWith('/tmp/foo');
    expect(createSession).toHaveBeenCalledTimes(2);
    expect(events).toEqual([
      'running:launch:0',
      'running:wait:0',
      'running:wait:1',
      'running:create:1',
      'success:create:1',
    ]);
    expect(useLaunchProgressStore.getState().directory).toBe('/tmp/foo');
  });

  it('launches opencode on the remote host when platform is remote', async () => {
    const createSession = vi
      .fn()
      .mockRejectedValueOnce(unreachable())
      .mockResolvedValueOnce({ id: 'remote-id' });
    const launchOpencodeInTmux = vi.fn().mockResolvedValue({ session: 'remote' });

    const promise = createSessionWithLaunch(
      { createSession, launchOpencodeInTmux, tmuxAvailable: true },
      { directory: '/remote/repo', platform: 'r-abc:opencode' },
    );

    await vi.runAllTimersAsync();
    await expect(promise).resolves.toEqual({ id: 'remote-id' });
    expect(launchOpencodeInTmux).toHaveBeenCalledWith('/remote/repo', 'abc');
  });

  it('launches locally when remoteId is "local" even for a remote-looking platform', async () => {
    const createSession = vi
      .fn()
      .mockRejectedValueOnce(unreachable())
      .mockResolvedValueOnce({ id: 'local-id' });
    const launchOpencodeInTmux = vi.fn().mockResolvedValue({ session: 'local' });

    const promise = createSessionWithLaunch(
      { createSession, launchOpencodeInTmux, tmuxAvailable: true },
      { directory: '/local/repo', platform: 'r-abc:opencode', remoteId: 'local' },
    );

    await vi.runAllTimersAsync();
    await expect(promise).resolves.toEqual({ id: 'local-id' });
    // remoteId 'local' short-circuits the remote branch: no remote id arg.
    expect(launchOpencodeInTmux).toHaveBeenCalledWith('/local/repo');
  });

  it('treats a malformed remote platform id as local', async () => {
    const createSession = vi
      .fn()
      .mockRejectedValueOnce(unreachable())
      .mockResolvedValueOnce({ id: 'mangled-id' });
    const launchOpencodeInTmux = vi.fn().mockResolvedValue({ session: 'foo' });

    const promise = createSessionWithLaunch(
      { createSession, launchOpencodeInTmux, tmuxAvailable: true },
      // ':' at index 2 -> end > 2 is false -> no remote id extracted.
      { directory: '/tmp/foo', platform: 'r-:opencode' },
    );

    await vi.runAllTimersAsync();
    await expect(promise).resolves.toEqual({ id: 'mangled-id' });
    expect(launchOpencodeInTmux).toHaveBeenCalledWith('/tmp/foo');
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
    const state = useLaunchProgressStore.getState();
    expect(state.phase).toBe('error');
    expect(state.error).toMatch(/Failed to launch opencode in tmux/);
  });

  it('falls back to an active main project after the requested directory launch fails', async () => {
    const createSession = vi
      .fn()
      .mockRejectedValueOnce(unreachable())
      .mockResolvedValueOnce({ id: 'main-id' });
    const launchOpencodeInTmux = vi
      .fn()
      .mockRejectedValueOnce(new Error('directory missing'));

    const res = await createSessionWithLaunch(
      { createSession, launchOpencodeInTmux, tmuxAvailable: true },
      { directory: '/tmp/.worktrees/repo/deleted', fallbackDirectory: '/tmp/repo' },
    );

    expect(res).toEqual({ id: 'main-id', directory: '/tmp/repo' });
    expect(createSession).toHaveBeenNthCalledWith(1, '/tmp/.worktrees/repo/deleted', undefined, undefined);
    expect(createSession).toHaveBeenNthCalledWith(2, '/tmp/repo', undefined, undefined);
    expect(launchOpencodeInTmux).toHaveBeenCalledTimes(1);
    expect(launchOpencodeInTmux).toHaveBeenCalledWith('/tmp/.worktrees/repo/deleted');
    const state = useLaunchProgressStore.getState();
    expect(state.phase).toBe('success');
    expect(state.directory).toBe('/tmp/repo');
  });

  it('starts opencode in the fallback directory when no active instance exists there', async () => {
    const createSession = vi
      .fn()
      .mockRejectedValueOnce(unreachable())
      .mockRejectedValueOnce(unreachable())
      .mockResolvedValueOnce({ id: 'main-id' });
    const launchOpencodeInTmux = vi
      .fn()
      .mockRejectedValueOnce(new Error('directory missing'))
      .mockResolvedValueOnce({ session: 'main' });

    const promise = createSessionWithLaunch(
      { createSession, launchOpencodeInTmux, tmuxAvailable: true },
      { directory: '/tmp/.worktrees/repo/deleted', fallbackDirectory: '/tmp/repo' },
    );

    await vi.runAllTimersAsync();
    const res = await promise;

    expect(res).toEqual({ id: 'main-id', directory: '/tmp/repo' });
    expect(launchOpencodeInTmux).toHaveBeenNthCalledWith(1, '/tmp/.worktrees/repo/deleted');
    expect(launchOpencodeInTmux).toHaveBeenNthCalledWith(2, '/tmp/repo');
    expect(createSession).toHaveBeenNthCalledWith(3, '/tmp/repo', undefined, undefined);
    expect(useLaunchProgressStore.getState().phase).toBe('success');
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
    // The launch step is skipped for externally-launched opencode.
    const state = useLaunchProgressStore.getState();
    expect(state.phase).toBe('success');
    expect(state.skipLaunch).toBe(true);
  });

  it('does not touch the progress store when reportProgress=false', async () => {
    const createSession = vi
      .fn()
      .mockRejectedValueOnce(unreachable())
      .mockResolvedValueOnce({ id: 'wt-session' });
    const launchOpencodeInTmux = vi.fn();

    const promise = createSessionWithLaunch(
      { createSession, launchOpencodeInTmux, tmuxAvailable: false },
      { directory: '/tmp/wt', alreadyLaunched: true, reportProgress: false },
    );
    await vi.runAllTimersAsync();
    const res = await promise;

    expect(res).toEqual({ id: 'wt-session' });
    expect(useLaunchProgressStore.getState().phase).toBe('idle');
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
    const state = useLaunchProgressStore.getState();
    expect(state.phase).toBe('error');
    expect(state.error).toBe('auth required');
  });

  it('reports a timeout error when retries exhaust on unreachable', async () => {
    const createSession = vi.fn().mockRejectedValue(unreachable());
    const launchOpencodeInTmux = vi.fn().mockResolvedValue({ session: 'foo' });

    const promise = createSessionWithLaunch(
      { createSession, launchOpencodeInTmux, tmuxAvailable: true },
      { directory: '/tmp/foo' },
    );
    const assertion = expect(promise).rejects.toMatchObject({ code: 'unreachable' });
    await vi.runAllTimersAsync();
    await assertion;

    const state = useLaunchProgressStore.getState();
    expect(state.phase).toBe('error');
    expect(state.error).toMatch(/did not start in time/);
    expect(state.attempt).toBe(5);
    expect(state.maxAttempts).toBe(5);
  });
});
