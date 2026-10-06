// @vitest-environment jsdom

import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import { RightPanel } from './RightPanel';
import { useUiStore } from '../lib/uiStore';
import * as upstreamApi from '../lib/upstreamApi';
import { useGitInfo } from '../lib/useGitInfo';
import type { Session } from '../lib/api';
import { useUpstreamPreferences } from '../lib/upstreamPreferences';

vi.mock('../lib/useGitInfo', () => ({
  useGitInfo: vi.fn(() => ({ infos: {}, loading: false, error: null })),
}));
vi.mock('../lib/pluginPanes', async (importOriginal) => ({
  ...await importOriginal<typeof import('../lib/pluginPanes')>(),
  usePluginPanes: () => ({ data: [] }),
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
  useUpstreamPreferences.setState(useUpstreamPreferences.getInitialState());
  useUiStore.persist.setOptions({
    storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
  });
  useUiStore.setState({
    changesSidebarOpenTabs: ['upstream'],
    changesSidebarTabOrder: ['info', 'session', 'working-tree', 'bookmarks', 'upstream'],
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
  await userEvent.click(screen.getByRole('radio', { name: 'closed' }));
  await waitFor(() => expect(upstreamApi.fetchPRs).toHaveBeenCalledTimes(2));
  await screen.findByText('PR 1');

  // The next session is still loading, then resolves to a sibling worktree.
  rerender(panel(undefined, 's2'));
  rerender(panel(session('s2', '/wt/repo/b', 'proj')));

  expect(screen.getByText('PR 1')).toBeInTheDocument();
  expect(screen.getByRole('radio', { name: 'closed' })).toBeChecked();
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

it('keeps upstream controls during detection and hides them only for an unsupported project', async () => {
  vi.spyOn(upstreamApi, 'fetchIssues').mockResolvedValue({
    issues: [], pagination: { page: 1, hasMore: false }, rateLimit: { limited: false },
  });
  const { rerender } = render(panel(session('s1', '/wt/repo/a', 'proj')));
  await screen.findByText('PR 1');
  await userEvent.click(screen.getByRole('tab', { name: 'Issues' }));
  await userEvent.click(screen.getByRole('radio', { name: 'closed' }));
  let resolve!: (upstreams: upstreamApi.Upstream[]) => void;
  vi.mocked(upstreamApi.fetchUpstreams).mockReturnValueOnce(new Promise((done) => { resolve = done; }));

  rerender(panel(session('s2', '/other', 'other-proj')));
  expect(screen.getByRole('tab', { name: 'Issues' })).toHaveAttribute('aria-selected', 'true');
  expect(screen.getByRole('radio', { name: 'closed' })).toBeChecked();
  expect(screen.queryByText('No supported upstream detected')).not.toBeInTheDocument();
  await act(async () => resolve([]));
  expect(screen.getByText('No supported upstream detected')).toBeInTheDocument();
  expect(screen.queryByRole('tab', { name: 'Issues' })).not.toBeInTheDocument();

  rerender(panel(session('s3', '/third', 'third-proj')));
  await waitFor(() => expect(upstreamApi.fetchUpstreams).toHaveBeenCalledTimes(3));
  expect(screen.getByRole('tab', { name: 'Issues' })).toHaveAttribute('aria-selected', 'true');
  expect(screen.getByRole('radio', { name: 'closed' })).toBeChecked();
});

it('does not flash upstream detection during a fast project switch', async () => {
  const { rerender } = render(panel(session('s1', '/wt/repo/a', 'proj')));
  await screen.findByText('PR 1');
  let resolve!: (upstreams: upstreamApi.Upstream[]) => void;
  vi.mocked(upstreamApi.fetchUpstreams).mockReturnValueOnce(new Promise((done) => { resolve = done; }));

  rerender(panel(session('s2', '/other', 'other-proj')));
  expect(screen.queryByRole('status', { name: 'Loading upstreams' })).not.toBeInTheDocument();
  await act(async () => resolve([{ remote: 'origin', host: 'github.com', type: 'github', repo: 'other/repo' }]));
  await screen.findByText('PR 1');
  expect(screen.queryByRole('status', { name: 'Loading upstreams' })).not.toBeInTheDocument();
});

it('shows a spinner in the list for slow upstream detection and resets it on the next project', async () => {
  const { rerender } = render(panel(session('s1', '/wt/repo/a', 'proj')));
  await screen.findByText('PR 1');
  vi.mocked(upstreamApi.fetchUpstreams).mockReturnValue(new Promise(() => {}));
  vi.useFakeTimers();
  try {
    rerender(panel(session('s2', '/other', 'other-proj')));
    expect(screen.queryByRole('status', { name: 'Loading upstreams' })).not.toBeInTheDocument();
    act(() => vi.advanceTimersByTime(200));
    const status = screen.getByRole('status', { name: 'Loading upstreams' });
    expect(status.closest('[role="tabpanel"]')).toBe(screen.getByRole('tabpanel'));
    expect(status.querySelector('.oc-loading-spinner')).toHaveAttribute('aria-hidden', 'true');
    expect(screen.queryByText('Detecting upstreams…')).not.toBeInTheDocument();
    rerender(panel(session('s3', '/third', 'third-proj')));
    expect(screen.queryByRole('status', { name: 'Loading upstreams' })).not.toBeInTheDocument();
    act(() => vi.advanceTimersByTime(200));
    expect(screen.getByRole('status', { name: 'Loading upstreams' })).toBeInTheDocument();
  } finally {
    vi.useRealTimers();
  }
});

it('treats unrelated non-git directories as different projects', async () => {
  const { rerender } = render(panel(session('s1', '/tmp/x', 'global')));
  await screen.findByText('PR 1');

  rerender(panel(session('s2', '/tmp/y', 'global')));

  await waitFor(() => expect(upstreamApi.fetchUpstreams).toHaveBeenCalledTimes(2));
});

it('disables launches while the next session is unresolved', async () => {
  const postHandle = vi.spyOn(upstreamApi, 'postHandle').mockResolvedValue({} as upstreamApi.HandleResponse);
  const { rerender } = render(panel(session('s1', '/wt/repo/a', 'proj')));
  await userEvent.click(await screen.findByText('PR 1'));
  expect(screen.getByTestId('launch-default')).toBeEnabled();

  // Still loading, then a stale copy of the previous session for the new id.
  for (const pending of [panel(undefined, 's2'), panel(session('s1', '/wt/repo/a', 'proj'), 's2')]) {
    rerender(pending);
    expect(screen.getByText('PR 1')).toBeInTheDocument();
    expect(screen.getByTestId('launch-default')).toBeDisabled();
    await userEvent.click(screen.getByTestId('launch-default'));
  }
  expect(postHandle).not.toHaveBeenCalled();

  rerender(panel(session('s2', '/wt/repo/b', 'proj')));
  await userEvent.click(screen.getByTestId('launch-default'));
  expect(postHandle).toHaveBeenCalledWith(expect.objectContaining({ dir: '/wt/repo/b', number: 1 }));
});

it('keeps loaded CI checks across a sibling switch', async () => {
  vi.mocked(upstreamApi.fetchPRs).mockResolvedValue({
    prs: [{ ...pr(1, 'feat-a'), headSha: 'abc' }],
    pagination: { page: 1, hasMore: false },
    rateLimit: { limited: false },
  });
  const fetchChecks = vi.spyOn(upstreamApi, 'fetchPRChecks').mockResolvedValue({
    state: 'success', checks: [{ name: 'build', state: 'success' }],
  });
  const { rerender } = render(panel(session('s1', '/wt/repo/a', 'proj')));
  await userEvent.click(await screen.findByText('PR 1'));
  expect(await screen.findByText('build')).toBeInTheDocument();

  rerender(panel(session('s2', '/wt/repo/b', 'proj')));

  expect(screen.getByText('build')).toBeInTheDocument();
  expect(fetchChecks).toHaveBeenCalledTimes(1);
});
