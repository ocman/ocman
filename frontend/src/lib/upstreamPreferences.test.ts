// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';

beforeEach(() => {
  localStorage.clear();
  vi.resetModules();
});

it('defaults to open PRs with independent issue filters', async () => {
  const { useUpstreamPreferences } = await import('./upstreamPreferences');
  expect(useUpstreamPreferences.getState().preferences).toEqual({
    tab: 'prs', prState: 'open', issueState: 'open', prMine: false, issueMine: false,
  });
});

it('restores every preference from storage after a page reload', async () => {
  const { useUpstreamPreferences } = await import('./upstreamPreferences');
  useUpstreamPreferences.getState().setPreferences({ prState: 'closed', prMine: true });
  useUpstreamPreferences.getState().setPreferences({ tab: 'issues', issueState: 'all', issueMine: true });
  const expected = { tab: 'issues', prState: 'closed', issueState: 'all', prMine: true, issueMine: true };
  expect(JSON.parse(localStorage.getItem('ocman-upstream-preferences')!).state).toEqual({ preferences: expected });

  vi.resetModules();
  const { useUpstreamPreferences: reloaded } = await import('./upstreamPreferences');
  expect(reloaded.getState().preferences).toEqual(expected);
  reloaded.getState().setPreferences({ tab: 'prs' });
  expect(reloaded.getState().preferences).toEqual({ ...expected, tab: 'prs' });
});
