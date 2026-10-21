// @vitest-environment jsdom
import { act, cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { StartupMonitor } from './StartupMonitor';
import { useStartupStatus } from '../lib/startupStatus';

const fetchMock = vi.fn();
const starting = { state: 'starting', phase: 'Rekeying session references', completed: 3, total: 20, elapsedSeconds: 42 };
const reply = (body: unknown, status = 200) => ({ ok: status === 200, status, json: () => Promise.resolve(body) });
async function flush() { await act(async () => { await Promise.resolve(); }); }
async function poll() { await act(async () => { await vi.advanceTimersByTimeAsync(2000); }); }

describe('StartupMonitor', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', fetchMock);
    fetchMock.mockReset();
    useStartupStatus.setState({ status: null, unreachable: false });
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('mounts the app immediately and records startup diagnostics without a startup page', async () => {
    fetchMock.mockResolvedValueOnce(reply(starting)).mockResolvedValue(reply({ state: 'ready' }));
    render(<StartupMonitor><p>Application</p></StartupMonitor>);
    expect(screen.getByText('Application')).toBeInTheDocument();
    await flush();
    expect(useStartupStatus.getState().status).toEqual(starting);
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith('/api/startup', expect.objectContaining({ cache: 'no-store' }));
    await poll();
    expect(useStartupStatus.getState().status?.state).toBe('ready');
  });

  it('preserves the composer and focus through restart, disconnect, failure and recovery', async () => {
    fetchMock.mockResolvedValueOnce(reply({ state: 'ready' }))
      .mockResolvedValueOnce(reply(starting)).mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(reply({ ...starting, state: 'failed', phase: 'Check logs' }))
      .mockResolvedValue(reply({ state: 'ready' }));
    render(<StartupMonitor><input aria-label="Composer" defaultValue="Unsent draft" /></StartupMonitor>);
    await flush();
    const composer = screen.getByRole('textbox', { name: 'Composer' });
    composer.focus();
    for (let i = 0; i < 4; i++) {
      await poll();
      expect(screen.getByRole('textbox', { name: 'Composer' })).toBe(composer);
      expect(composer).toHaveValue('Unsent draft');
      expect(composer).toHaveFocus();
    }
    expect(screen.queryByRole('heading', { name: 'Starting Ocman' })).not.toBeInTheDocument();
    expect(useStartupStatus.getState()).toMatchObject({ status: { state: 'ready' }, unreachable: false });
  });

  it('retries invalid status responses without replacing the app', async () => {
    fetchMock.mockResolvedValueOnce(reply({ state: 'invalid' })).mockResolvedValueOnce(reply({}, 503))
      .mockResolvedValue(reply({ ...starting, total: 0, completed: 500 }));
    render(<StartupMonitor>Application</StartupMonitor>);
    await flush();
    expect(useStartupStatus.getState().unreachable).toBe(true);
    await poll();
    expect(useStartupStatus.getState().unreachable).toBe(true);
    await poll();
    expect(useStartupStatus.getState()).toMatchObject({ unreachable: false, status: { total: 0, completed: 500 } });
    expect(screen.getByText('Application')).toBeInTheDocument();
  });

  it('accepts standalone backends without a startup endpoint', async () => {
    fetchMock.mockResolvedValue(reply({}, 404));
    render(<StartupMonitor>Application</StartupMonitor>);
    await flush();
    expect(useStartupStatus.getState().status?.state).toBe('ready');
  });

  it('aborts stalled requests and stops polling on unmount', async () => {
    let signal: AbortSignal | undefined;
    fetchMock.mockImplementation((_url, options) => new Promise((_resolve, reject) => {
      signal = options.signal;
      signal?.addEventListener('abort', () => reject(new Error('aborted')));
    }));
    const view = render(<StartupMonitor>Application</StartupMonitor>);
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(signal?.aborted).toBe(true);
    expect(useStartupStatus.getState().unreachable).toBe(true);
    await poll();
    const calls = fetchMock.mock.calls.length;
    view.unmount();
    expect(signal?.aborted).toBe(true);
    await poll();
    expect(fetchMock).toHaveBeenCalledTimes(calls);
  });
});
