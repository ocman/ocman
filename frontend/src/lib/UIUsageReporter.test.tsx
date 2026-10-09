// @vitest-environment jsdom
import { act, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { apiFetch } from './api.requests';
import { UIUsageReporter } from './UIUsageReporter';

vi.mock('./api.requests', () => ({
  apiFetch: vi.fn(),
  readJSON: (response: Response) => response.json(),
  raiseForUnauthorized: vi.fn().mockResolvedValue(undefined),
}));

const send = vi.mocked(apiFetch);
const payloads = () => send.mock.calls.map(([, init]) => JSON.parse(String(init?.body)) as { intervals: { start: number; end: number }[] });
async function flush() { await act(async () => { await Promise.resolve(); }); }
async function advance(ms: number) { await act(async () => { await vi.advanceTimersByTimeAsync(ms); }); }

describe('UIUsageReporter', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(1_000_000);
    vi.clearAllMocks();
    Object.defineProperty(document, 'hidden', { configurable: true, value: false });
    vi.spyOn(document, 'hasFocus').mockReturnValue(true);
    send.mockImplementation(async () => new Response(JSON.stringify({ now: Date.now() }), { status: 200 }));
  });
  afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); });

  it('calibrates server time, sends minutes, flushes blur, and stops while idle', async () => {
    const { unmount } = render(<UIUsageReporter />);
    await flush();
    expect(payloads()).toEqual([{ intervals: [] }]);
    await advance(60_000);
    expect(payloads().at(-1)?.intervals).toEqual([{ start: 1_000_000, end: 1_060_000 }]);
    await advance(10_000);
    vi.mocked(document.hasFocus).mockReturnValue(false);
    window.dispatchEvent(new Event('blur'));
    await flush();
    expect(payloads().at(-1)?.intervals).toEqual([{ start: 1_060_000, end: 1_070_000 }]);
    const count = send.mock.calls.length;
    await advance(120_000);
    expect(send).toHaveBeenCalledTimes(count);
    vi.mocked(document.hasFocus).mockReturnValue(true);
    window.dispatchEvent(new Event('focus'));
    await advance(360_000);
    const afterIdle = send.mock.calls.length;
    await advance(120_000);
    expect(send).toHaveBeenCalledTimes(afterIdle);
    window.dispatchEvent(new Event('scroll'));
    await advance(60_000);
    expect(send.mock.calls.length).toBeGreaterThan(afterIdle);
    unmount();
  });

  it('retries failed intervals without sending an interaction event per request', async () => {
    const { unmount } = render(<UIUsageReporter />);
    await flush();
    send.mockRejectedValueOnce(new Error('offline'));
    await advance(60_000);
    window.dispatchEvent(new Event('keydown'));
    window.dispatchEvent(new Event('pointerdown'));
    expect(send).toHaveBeenCalledTimes(2);
    await advance(60_000);
    expect(payloads().at(-1)?.intervals).toEqual([{ start: 1_010_000, end: 1_120_000 }]);
    unmount();
  });

  it('flushes on hiding/pagehide and removes listeners and timers', async () => {
    const { unmount } = render(<UIUsageReporter />);
    await flush();
    await advance(10_000);
    Object.defineProperty(document, 'hidden', { configurable: true, value: true });
    document.dispatchEvent(new Event('visibilitychange'));
    await flush();
    expect(send.mock.calls.at(-1)?.[1]?.keepalive).toBe(true);
    expect(payloads().at(-1)?.intervals).toEqual([{ start: 1_000_000, end: 1_010_000 }]);
    window.dispatchEvent(new Event('pagehide'));
    await flush();
    unmount();
    const count = send.mock.calls.length;
    window.dispatchEvent(new Event('focus'));
    await advance(120_000);
    expect(send).toHaveBeenCalledTimes(count);
  });

  it('recalibrates after an expired/clock-shifted request', async () => {
    const { unmount } = render(<UIUsageReporter />);
    await flush();
    send.mockResolvedValueOnce(new Response('', { status: 400 }));
    await advance(60_000);
    await advance(60_000);
    expect(payloads().at(-1)?.intervals).toEqual([]);
    await advance(60_000);
    expect(payloads().at(-1)?.intervals).toEqual([{ start: 1_120_000, end: 1_180_000 }]);
    unmount();
  });

  it('resumes usage when a nested scroll container dispatches a non-bubbling scroll', async () => {
    const { unmount } = render(<UIUsageReporter />);
    await flush();
    await advance(420_000);
    const idleCount = send.mock.calls.length;
    const pane = document.createElement('div');
    document.body.append(pane);
    pane.dispatchEvent(new Event('scroll', { bubbles: false }));
    await advance(60_000);
    expect(send.mock.calls.length).toBeGreaterThan(idleCount);
    expect(payloads().at(-1)?.intervals).toEqual([{ start: 1_420_000, end: 1_480_000 }]);
    pane.remove();
    unmount();
  });
});
