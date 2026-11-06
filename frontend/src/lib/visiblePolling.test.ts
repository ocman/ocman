// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { startVisiblePolling } from './visiblePolling';

let hidden = false;
let stop: (() => void) | undefined;
beforeEach(() => {
  vi.useFakeTimers(); hidden = false;
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
});
afterEach(() => { stop?.(); stop = undefined; vi.useRealTimers(); vi.restoreAllMocks(); });
function visibility(value: boolean) { hidden = value; document.dispatchEvent(new Event('visibilitychange')); }
async function settle() { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); }

it('serializes focus/reconnect, retries failures and keeps the visible cadence', async () => {
  let finish!: () => void;
  const load = vi.fn().mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve; }))
    .mockRejectedValueOnce(new Error('offline')).mockResolvedValue(undefined);
  stop = startVisiblePolling(load, 5000);
  window.dispatchEvent(new Event('focus')); window.dispatchEvent(new Event('online'));
  await vi.advanceTimersByTimeAsync(20_000);
  expect(load).toHaveBeenCalledTimes(1);
  finish(); await settle();
  await vi.advanceTimersByTimeAsync(5000);
  expect(load).toHaveBeenCalledTimes(2);
  await vi.advanceTimersByTimeAsync(5000);
  expect(load).toHaveBeenCalledTimes(3);
});

it('reconciles after a pre-hide read completes, without overlapping it', async () => {
  let finish!: () => void;
  const load = vi.fn().mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve; })).mockResolvedValue(undefined);
  stop = startVisiblePolling(load, 5000);
  visibility(true); visibility(false);
  expect(load).toHaveBeenCalledTimes(1);
  finish(); await settle();
  expect(load).toHaveBeenCalledTimes(2);
  visibility(true);
  await vi.advanceTimersByTimeAsync(60_000);
  window.dispatchEvent(new Event('focus')); window.dispatchEvent(new Event('online'));
  expect(load).toHaveBeenCalledTimes(2);
  visibility(false); await settle();
  expect(load).toHaveBeenCalledTimes(3);
});

it('does not schedule work after disposal during an in-flight read', async () => {
  let finish!: () => void;
  const load = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
  stop = startVisiblePolling(load, 5000);
  stop(); finish(); await settle();
  visibility(true); visibility(false); window.dispatchEvent(new Event('online')); window.dispatchEvent(new Event('focus'));
  await vi.advanceTimersByTimeAsync(20_000);
  expect(load).toHaveBeenCalledTimes(1);
});
