// @vitest-environment jsdom

import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const getSystemStats = vi.fn();

vi.mock('../lib/apiStore', () => ({
  useApiStore: (selector: (state: { getSystemStats: typeof getSystemStats }) => unknown) =>
    selector({ getSystemStats }),
}));

vi.mock('../lib/useLongTaskMonitor', () => ({
  useLongTaskMonitor: () => ({ count: 0, maxMs: 0 }),
}));

import { BackendStats } from './BackendStats';

let hidden = false;

function setHidden(next: boolean) {
  hidden = next;
  document.dispatchEvent(new Event('visibilitychange'));
}

/** Flush the promise chain of an in-flight getSystemStats under fake timers. */
async function flush() {
  await act(async () => { await Promise.resolve(); await Promise.resolve(); });
}

/** Heap reads are the "frontend memory sampling" side of FR-10. */
let heapReads = 0;

beforeEach(() => {
  vi.useFakeTimers();
  hidden = false;
  heapReads = 0;
  getSystemStats.mockReset();
  getSystemStats.mockImplementation(() =>
    Promise.resolve({ memory: { heapAlloc: 42 * 1024 * 1024 }, uptime: 61 }));
  Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden });
  Object.defineProperty(performance, 'memory', {
    configurable: true,
    get: () => {
      heapReads += 1;
      return { usedJSHeapSize: 7 * 1024 * 1024, totalJSHeapSize: 0, jsHeapSizeLimit: 100 * 1024 * 1024 };
    },
  });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('BackendStats visibility gating (FR-10)', () => {
  it('issues no requests and samples no heap while the document is hidden', async () => {
    hidden = true;
    render(<BackendStats />);
    await flush();

    await act(async () => { vi.advanceTimersByTime(60_000); });
    await flush();

    expect(getSystemStats).not.toHaveBeenCalled();
    expect(heapReads).toBe(0);
  });

  it('stops polling when the document becomes hidden mid-flight', async () => {
    render(<BackendStats />);
    await flush();
    expect(getSystemStats).toHaveBeenCalledTimes(1);

    act(() => setHidden(true));
    await act(async () => { vi.advanceTimersByTime(60_000); });
    await flush();

    expect(getSystemStats).toHaveBeenCalledTimes(1);
  });

  it('fetches immediately when the document becomes visible again', async () => {
    hidden = true;
    render(<BackendStats />);
    await flush();
    expect(getSystemStats).not.toHaveBeenCalled();

    await act(async () => { setHidden(false); });
    await flush();

    expect(getSystemStats).toHaveBeenCalledTimes(1);
  });

  it('keeps the existing 5s cadence while visible', async () => {
    render(<BackendStats />);
    await flush();
    expect(getSystemStats).toHaveBeenCalledTimes(1);

    await act(async () => { vi.advanceTimersByTime(5000); });
    await flush();
    expect(getSystemStats).toHaveBeenCalledTimes(2);

    await act(async () => { vi.advanceTimersByTime(5000); });
    await flush();
    expect(getSystemStats).toHaveBeenCalledTimes(3);
  });

  it('resumes the 5s cadence after returning to visible', async () => {
    render(<BackendStats />);
    await flush();

    act(() => setHidden(true));
    await act(async () => { vi.advanceTimersByTime(60_000); });
    await flush();
    expect(getSystemStats).toHaveBeenCalledTimes(1);

    await act(async () => { setHidden(false); });
    await flush();
    expect(getSystemStats).toHaveBeenCalledTimes(2);

    await act(async () => { vi.advanceTimersByTime(5000); });
    await flush();
    expect(getSystemStats).toHaveBeenCalledTimes(3);
  });

  it('retains the last successful values while hidden', async () => {
    render(<BackendStats />);
    await flush();
    expect(screen.getByTitle('Backend Memory').parentElement).toHaveTextContent('be: 42MB');
    expect(screen.getByTitle('Uptime').parentElement).toHaveTextContent('up: 1m 1s');

    act(() => setHidden(true));
    await act(async () => { vi.advanceTimersByTime(60_000); });
    await flush();

    expect(screen.getByTitle('Backend Memory').parentElement).toHaveTextContent('be: 42MB');
    expect(screen.getByTitle('Uptime').parentElement).toHaveTextContent('up: 1m 1s');
  });

  it('leaves no timers or visibility listeners behind on unmount', async () => {
    const add = vi.spyOn(document, 'addEventListener');
    const remove = vi.spyOn(document, 'removeEventListener');

    const { unmount } = render(<BackendStats />);
    await flush();
    expect(vi.getTimerCount()).toBeGreaterThan(0);

    unmount();

    expect(vi.getTimerCount()).toBe(0);
    const added = add.mock.calls.filter(([type]) => type === 'visibilitychange');
    const removed = remove.mock.calls.filter(([type]) => type === 'visibilitychange');
    expect(added.length).toBeGreaterThan(0);
    expect(removed.length).toBe(added.length);

    getSystemStats.mockClear();
    await act(async () => { vi.advanceTimersByTime(60_000); });
    await flush();
    expect(getSystemStats).not.toHaveBeenCalled();
  });

  it('still hides itself when the request fails', async () => {
    getSystemStats.mockRejectedValue(new Error('boom'));
    const { container } = render(<BackendStats />);
    await flush();
    expect(container).toBeEmptyDOMElement();
  });
});
