import { expect, it, vi } from 'vitest';
import { eventRefresh } from './eventRefresh';

it('bounds continuous event delay and retains a follow-up without overlapping reads', async () => {
  vi.useFakeTimers();
  let finish!: () => void;
  const fetch = vi.fn().mockImplementationOnce(() => new Promise<void>(resolve => { finish = resolve; })).mockResolvedValue(undefined);
  const refresh = eventRefresh(fetch);
  try {
    for (let i = 0; i < 5; i++) {
      refresh.schedule();
      await vi.advanceTimersByTimeAsync(100);
    }
    expect(fetch).toHaveBeenCalledTimes(1);
    for (let i = 0; i < 10; i++) {
      refresh.schedule();
      await vi.advanceTimersByTimeAsync(100);
    }
    expect(fetch).toHaveBeenCalledTimes(1);
    finish();
    await vi.advanceTimersByTimeAsync(0);
    expect(fetch).toHaveBeenCalledTimes(2);
  } finally { refresh.dispose(); vi.useRealTimers(); }
});
