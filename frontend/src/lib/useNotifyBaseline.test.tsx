// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { NotifyEntry } from './api';
import { queryClient } from './queryClient';
import { useFaviconNotify } from './useFaviconNotify';
import { useNotifyBaseline, type NotifyBaselineOptions } from './useNotifyBaseline';
import { __resetForTests, notifyQueryKey, type NotifySnapshot } from './useNotifyData';

vi.mock('./api', () => ({ api: { sessionsNotify: vi.fn(() => new Promise(() => {})) } }));

let hidden = false;
function setHidden(value: boolean) {
  hidden = value;
  act(() => { document.dispatchEvent(new Event('visibilitychange')); });
}

let fetchedAt = 0;
async function publish(entries: NotifyEntry[]) {
  await act(async () => {
    queryClient.setQueryData<NotifySnapshot>(notifyQueryKey, { entries, fetchedAt: ++fetchedAt });
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

const waiting: NotifyEntry = { id: 's1', status: 'waiting', seen: false };
const asking: NotifyEntry = { id: 's2', status: 'busy', seen: false, pendingQuestion: true };

function mount(opts?: NotifyBaselineOptions, resnapshot = false) {
  const calls: { ids: string[]; baseline: Map<string, string> | null }[] = [];
  const hook = renderHook(() => useNotifyBaseline((sessions, baseline) => {
    calls.push({ ids: sessions.map((s) => s.id), baseline: baseline && new Map(baseline) });
    return resnapshot;
  }, opts));
  return { calls, unmount: hook.unmount };
}

beforeEach(() => {
  hidden = false;
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
});

afterEach(() => {
  __resetForTests();
  vi.restoreAllMocks();
  document.title = '';
});

it('checks every new snapshot but not the one cached at mount', async () => {
  await publish([waiting]);
  const { calls, unmount } = mount();
  expect(calls).toEqual([]);
  await publish([waiting, asking]);
  await publish([waiting, asking]);
  expect(calls.map((c) => c.ids)).toEqual([['s1', 's2'], ['s1', 's2']]);
  expect(calls[0].baseline).toBeNull();
  unmount();
});

it('snapshots on hide, checks once, and clears on show', async () => {
  const onVisibleReset = vi.fn();
  const { calls, unmount } = mount({ onVisibleReset });
  await publish([waiting]);
  setHidden(true);
  expect(calls.at(-1)).toEqual({ ids: ['s1'], baseline: new Map([['s1', 'waiting|0|0']]) });
  setHidden(false);
  expect(onVisibleReset).toHaveBeenCalledOnce();
  await publish([waiting]);
  expect(calls.at(-1)?.baseline).toBeNull();
  unmount();
});

it('re-snapshots when the check asks for it', async () => {
  const { calls, unmount } = mount(undefined, true);
  await publish([waiting]);
  await publish([asking]);
  expect(calls[1].baseline).toEqual(new Map([['s1', 'waiting|0|0']]));
  unmount();
});

it.each([
  ['hidden', false, null],
  ['hidden', true, new Map([['s1', 'waiting|0|0']])],
  ['always', false, new Map([['s1', 'waiting|0|0']])],
  ['never', true, null],
] as const)('takes the %s initial baseline (hidden=%s)', async (initialBaseline, isHidden, expected) => {
  hidden = isHidden;
  await publish([waiting]);
  const { calls, unmount } = mount({ initialBaseline });
  await publish([waiting]);
  expect(calls[0].baseline).toEqual(expected);
  unmount();
});

it('supports a custom key, mount checks and ignoring visibility', async () => {
  await publish([waiting]);
  const shown = mount({ key: (s) => s.status, initialBaseline: 'always', checkWhenShown: true });
  expect(shown.calls).toEqual([{ ids: ['s1'], baseline: new Map([['s1', 'waiting']]) }]);
  const fixed = mount({ initialBaseline: 'always', ignoreVisibility: true });
  setHidden(true);
  setHidden(false);
  expect(shown.calls.at(-1)).toEqual({ ids: ['s1'], baseline: null });
  await publish([waiting]);
  expect(fixed.calls).toEqual([{ ids: ['s1'], baseline: new Map([['s1', 'waiting|0|0']]) }]);
  shown.unmount();
  fixed.unmount();
});

it('marks the favicon and title for prompts and unseen completions', async () => {
  document.title = 'ocman';
  const { unmount } = renderHook(() => useFaviconNotify());
  await publish([asking]);
  expect(document.title).toBe('(!) ocman');
  expect(document.querySelector<HTMLLinkElement>('link[rel="icon"][type="image/svg+xml"]')?.href).toContain('/favicon-prompt.svg');
  await publish([waiting]);
  expect(document.title).toBe('ocman');
  setHidden(true);
  await publish([waiting, { id: 's3', status: 'error', seen: false }]);
  expect(document.title).toBe('(1) ocman');
  setHidden(false);
  expect(document.title).toBe('ocman');
  await publish([asking]);
  unmount();
  expect(document.title).toBe('ocman');
  expect(document.querySelector<HTMLLinkElement>('link[rel="apple-touch-icon"]')?.href).toContain('/apple-touch-icon.png');
});
