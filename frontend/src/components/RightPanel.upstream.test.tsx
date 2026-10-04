// @vitest-environment jsdom

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import { RightPanel } from './RightPanel';
import { useUiStore } from '../lib/uiStore';
import * as upstreamApi from '../lib/upstreamApi';
import { useGitInfo } from '../lib/useGitInfo';
import type { Session } from '../lib/api';

vi.mock('../lib/useGitInfo', () => ({
  useGitInfo: vi.fn(() => ({ infos: {}, loading: false, error: null })),
}));
vi.mock('../lib/useBeadsStatus', () => ({
  useBeadsStatus: () => ({ data: undefined, error: null, isFetching: false, refetch: vi.fn() }),
}));

const pr = (number: number, branch: string): upstreamApi.PR => ({
  number, title: `PR ${number}`, body: '', author: 'a', status: 'open', updatedAt: '2026-01-01T00:00:00Z',
  labels: null, assignees: null, requestedReviewers: null, branch, url: '', host: 'github.com', repo: 'a/repo',
  crossFork: false,
});

const session = (id: string, directory: string, projectId: string) =>
  ({ id, directory, projectId, remoteId: '' }) as Session;

const panel = (s: Session | undefined, id = s?.id ?? 'pending') => (
  <RightPanel
    sessionId={id}
    platformId="opencode"
    directory={s?.directory}
    session={s}
    messageBookmarkGroups={[]}
    selectedMessageBookmarkKey={null}
    onRemoveMessageBookmark={vi.fn()}
    onScrollToMessageBookmark={vi.fn()}
  />
);

beforeEach(() => {
  vi.restoreAllMocks();
  useUiStore.persist.setOptions({
    storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
  });
  useUiStore.setState({
    changesSidebarOpenTabs: ['upstream'],
    changesSidebarTabOrder: ['info', 'session', 'working-tree', 'bookmarks', 'upstream', 'beads'],
    changesSidebarTabSizes: {},
  });
  vi.spyOn(upstreamApi, 'fetchUpstreams').mockResolvedValue([
    { remote: 'origin', host: 'github.com', type: 'github', repo: 'a/repo' },
  ]);
  vi.spyOn(upstreamApi, 'fetchPRs').mockResolvedValue({
    prs: [pr(1, 'feat-a'), pr(2, 'feat-b')],
    pagination: { page: 1, hasMore: false },
    rateLimit: { limited: false },
  });
  vi.mocked(useGitInfo).mockImplementation((dirs) => ({
    infos: Object.fromEntries((dirs ?? []).map((d) => [d, { branch: d.endsWith('/a') ? 'feat-a' : 'feat-b' }])),
    loading: false,
    error: null,
  }) as unknown as ReturnType<typeof useGitInfo>);
});

it('keeps the PR list when switching to another session of the same project', async () => {
  const { rerender } = render(panel(session('s1', '/wt/repo/a', 'proj')));
  expect(await screen.findByText('PR 1')).toBeInTheDocument();
  await userEvent.click(screen.getByTestId('upstream-filter-closed'));
  await waitFor(() => expect(upstreamApi.fetchPRs).toHaveBeenCalledTimes(2));
  await screen.findByText('PR 1');

  // The next session is still loading, then resolves to a sibling worktree.
  rerender(panel(undefined, 's2'));
  rerender(panel(session('s2', '/wt/repo/b', 'proj')));

  expect(screen.getByText('PR 1')).toBeInTheDocument();
  expect(screen.getByTestId('upstream-filter-closed')).toHaveAttribute('aria-checked', 'true');
  await waitFor(() =>
    expect(screen.getByText('PR 2').closest('li')).toHaveClass('current-branch'),
  );
  expect(screen.getByText('PR 1').closest('li')).not.toHaveClass('current-branch');
  expect(upstreamApi.fetchUpstreams).toHaveBeenCalledTimes(1);
  expect(upstreamApi.fetchPRs).toHaveBeenCalledTimes(2);
});

it('reloads for a session of a different project', async () => {
  const { rerender } = render(panel(session('s1', '/wt/repo/a', 'proj')));
  await screen.findByText('PR 1');

  rerender(panel(session('s2', '/other', 'other-proj')));

  await waitFor(() => expect(upstreamApi.fetchUpstreams).toHaveBeenCalledTimes(2));
  expect(vi.mocked(upstreamApi.fetchUpstreams).mock.calls[1][0]).toBe('/other');
});

it('treats unrelated non-git directories as different projects', async () => {
  const { rerender } = render(panel(session('s1', '/tmp/x', 'global')));
  await screen.findByText('PR 1');

  rerender(panel(session('s2', '/tmp/y', 'global')));

  await waitFor(() => expect(upstreamApi.fetchUpstreams).toHaveBeenCalledTimes(2));
});
