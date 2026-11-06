// @vitest-environment jsdom
import { act, render } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { BetaFeaturesSync } from './BetaFeaturesSync';
import { fetchJSON } from '../lib/api.requests';

vi.mock('../lib/api.requests', () => ({ fetchJSON: vi.fn(async () => ({ factory: false })), postJSON: vi.fn() }));
let hidden = false;
beforeEach(() => {
  vi.useFakeTimers();
  vi.mocked(fetchJSON).mockClear();
  hidden = false;
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
});
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });
async function visibility(value: boolean) {
  hidden = value;
  await act(async () => { document.dispatchEvent(new Event('visibilitychange')); });
}

it('does no hidden periodic fetching and reconciles immediately on return', async () => {
  const view = render(<BetaFeaturesSync />);
  await act(async () => {});
  expect(fetchJSON).toHaveBeenCalledTimes(1);
  await visibility(true);
  await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
  expect(fetchJSON).toHaveBeenCalledTimes(1);
  await visibility(false);
  expect(fetchJSON).toHaveBeenCalledTimes(2);
  view.unmount();
  await act(async () => { await vi.advanceTimersByTimeAsync(20_000); window.dispatchEvent(new Event('focus')); });
  expect(fetchJSON).toHaveBeenCalledTimes(2);
});

it('does not load on hidden mount, deduplicates focus/resume and cleans up in-flight work', async () => {
  hidden = true;
  let finish!: (value: { factory: boolean }) => void;
  vi.mocked(fetchJSON).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
  const view = render(<BetaFeaturesSync />);
  expect(fetchJSON).not.toHaveBeenCalled();
  await visibility(false);
  await act(async () => {
    window.dispatchEvent(new Event('focus'));
    window.dispatchEvent(new Event('online'));
    await vi.advanceTimersByTimeAsync(30_000);
  });
  expect(fetchJSON).toHaveBeenCalledTimes(1);
  view.unmount();
  await act(async () => { finish({ factory: true }); });
  await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
  expect(fetchJSON).toHaveBeenCalledTimes(1);
});
