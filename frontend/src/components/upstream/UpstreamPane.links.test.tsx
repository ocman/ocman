// @vitest-environment jsdom
import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import { UpstreamPane } from './UpstreamPane';
import { renderWithQuery as render } from '../../test/renderWithQuery';
import { ConfirmContext } from '../../lib/useConfirm';
import { prWorkflowApi, type PRLink } from '../../lib/prWorkflowApi';
import * as api from '../../lib/upstreamApi';
import { queryClient } from '../../lib/queryClient';
import { useUpstreamPreferences } from '../../lib/upstreamPreferences';
import { __handleSessionChangedForTests } from '../../lib/useGlobalEvents';

vi.mock('../../lib/prWorkflowApi', () => ({ prWorkflowApi: { links: vi.fn() } }));
vi.mock('../../lib/useGitInfo', () => ({ useGitInfo: () => ({ infos: {} }) }));
vi.mock('./LinkedPRStatus', () => ({ LinkedPRStatus: () => null }));
vi.mock('./PRRow', () => ({ PRRow: ({ pr }: { pr: api.PR }) => <li>{pr.title}</li> }));

const upstream: api.Upstream = { remote: 'origin', host: 'github.com', type: 'github', repo: 'team/repo' };
const link: PRLink = {
  platform: 'r-box:opencode', sessionId: 's1', remoteId: 'box', directory: '/repo', remote: upstream,
  number: 42, watching: false,
  pr: { number: 42, title: 'Newly created PR', body: '', author: 'alice', status: 'open',
    url: 'https://github.com/team/repo/pull/42', branch: 'feature', updatedAt: '',
    host: upstream.host, repo: upstream.repo, labels: [], assignees: [], requestedReviewers: [], crossFork: false },
};

beforeEach(() => {
  vi.restoreAllMocks();
  vi.clearAllMocks();
  queryClient.clear();
  useUpstreamPreferences.setState(useUpstreamPreferences.getInitialState());
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [] });
});

function view(version = 0, upstreams = [upstream]) {
  return <ConfirmContext.Provider value={vi.fn()}><UpstreamPane
    sessionId="s1" launchPlatform={link.platform} directory="/repo" remoteId="box" upstreams={upstreams} linksVersion={version}
  /></ConfirmContext.Provider>;
}

function emit() {
  __handleSessionChangedForTests(JSON.stringify({ sessionID: 's1', platform: link.platform }));
}

it('fetches a fresh project list when a newly linked PR arrives through a live event', async () => {
  const fetch = vi.spyOn(api, 'fetchPRs').mockImplementation(async (params) => ({
    prs: params.refresh ? [link.pr] : [], pagination: { page: 1, hasMore: false }, rateLimit: { limited: false },
  }));
  render(view());
  await screen.findByText('No pull requests.');
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [link] });
  act(() => __handleSessionChangedForTests(JSON.stringify({ sessionID: 's1', platform: link.platform })));
  await screen.findByRole('link', { name: '#42 Newly created PR' });
  await waitFor(() => expect(within(screen.getByTestId('upstream-prs-list')).getByText(link.pr.title)).toBeInTheDocument());
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(fetch.mock.calls[1][0].refresh).toBe(true);

  // Watch/status updates must not spend more forge quota.
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [{ ...link, watching: true }] });
  await act(async () => __handleSessionChangedForTests(JSON.stringify({ sessionID: 's1', platform: link.platform })));
  expect(fetch).toHaveBeenCalledTimes(2);
});

it('also reconciles a manual link and only refreshes the matching owner, host and repository', async () => {
  const fetch = vi.spyOn(api, 'fetchPRs').mockImplementation(async (params) => ({
    prs: params.refresh ? [link.pr] : [], pagination: { page: 1, hasMore: false }, rateLimit: { limited: false },
  }));
  const mirror = { ...upstream, remote: 'mirror', repo: 'other/repo' };
  const { rerender } = render(view(0, [upstream, mirror]));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  for (const mismatched of [
    { ...link, remoteId: 'other-owner' },
    { ...link, remote: { ...upstream, host: 'other.example' } },
    { ...link, remote: { ...upstream, repo: 'unrelated/repo' } },
  ]) {
    vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [mismatched] });
    await act(async () => emit());
    expect(fetch).toHaveBeenCalledTimes(2);
  }
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [link] });
  rerender(view(1, [upstream, mirror]));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(3));
  expect(fetch.mock.calls[2][0]).toMatchObject({ remote: 'origin', remoteId: 'box', refresh: true });
});

it('does not refresh when the linked PR is already in the project list', async () => {
  const fetch = vi.spyOn(api, 'fetchPRs').mockResolvedValue({
    prs: [link.pr], pagination: { page: 1, hasMore: false }, rateLimit: { limited: false },
  });
  render(view());
  await screen.findByText(link.pr.title);
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [link] });
  await act(async () => emit());
  expect(fetch).toHaveBeenCalledOnce();
});

it('waits for an older in-flight list before fetching fresh data', async () => {
  let finish!: (value: api.ListPRsResponse) => void;
  const fetch = vi.spyOn(api, 'fetchPRs')
    .mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }))
    .mockResolvedValue({ prs: [link.pr], pagination: { page: 1, hasMore: false }, rateLimit: { limited: false } });
  render(view());
  await waitFor(() => expect(fetch).toHaveBeenCalledOnce());
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [link] });
  await act(async () => emit());
  expect(fetch).toHaveBeenCalledOnce();
  await act(async () => finish({ prs: [], pagination: { page: 1, hasMore: false }, rateLimit: { limited: false } }));
  await waitFor(() => expect(within(screen.getByTestId('upstream-prs-list')).getByText(link.pr.title)).toBeInTheDocument());
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(fetch.mock.calls[1][0].refresh).toBe(true);
});

it('does not loop on filtered-out links, removals or failed refreshes', async () => {
  const fetch = vi.spyOn(api, 'fetchPRs').mockResolvedValue({
    prs: [], pagination: { page: 1, hasMore: false }, rateLimit: { limited: false },
  });
  useUpstreamPreferences.getState().setPreferences({ prState: 'closed' });
  render(view());
  await screen.findByText('No pull requests.');
  const second = { ...link, number: 43, pr: { ...link.pr, number: 43, title: 'Second PR' } };
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [link, second] });
  await act(async () => emit());
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  expect(fetch.mock.calls[1][0]).toMatchObject({ state: 'closed', page: 1, refresh: true });
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [link] });
  await act(async () => emit());
  expect(fetch).toHaveBeenCalledTimes(2);
  fetch.mockRejectedValue(new Error('offline'));
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [link, second] });
  await act(async () => emit());
  await screen.findByText('offline');
  await act(async () => emit());
  expect(fetch).toHaveBeenCalledTimes(3);
});

it('honors a rate-limited list without triggering an automatic retry', async () => {
  const fetch = vi.spyOn(api, 'fetchPRs').mockResolvedValue({
    prs: [], pagination: { page: 1, hasMore: false }, rateLimit: { limited: true, resetAt: '2099-01-01T00:00:00Z' },
  });
  render(view());
  await screen.findByText('No pull requests.');
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [link] });
  await act(async () => emit());
  expect(fetch).toHaveBeenCalledOnce();
});

it('also honors rate-limit errors returned by the backend', async () => {
  const fetch = vi.spyOn(api, 'fetchPRs').mockRejectedValue(new api.UpstreamApiError({
    error: { code: 'rate_limited', message: 'Slow down', retryAfter: '2099-01-01T00:00:00Z' },
  }, 429));
  render(view());
  await screen.findByRole('alert');
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [link] });
  await act(async () => emit());
  expect(fetch).toHaveBeenCalledOnce();
});

it('does not carry the previous session links into another owner', async () => {
  const fetch = vi.spyOn(api, 'fetchPRs').mockResolvedValue({
    prs: [], pagination: { page: 1, hasMore: false }, rateLimit: { limited: false },
  });
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [link] });
  const { rerender } = render(view());
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [] });
  rerender(<ConfirmContext.Provider value={vi.fn()}><UpstreamPane
    sessionId="s2" launchPlatform="r-other:opencode" directory="/repo" remoteId="other" upstreams={[upstream]}
  /></ConfirmContext.Provider>);
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(3));
  await act(async () => {});
  expect(fetch.mock.calls[2][0]).toMatchObject({ remoteId: 'other', refresh: false });
  expect(fetch).toHaveBeenCalledTimes(3);
});

it('leaves the issue list alone when returning from the PR tab', async () => {
  const fetch = vi.spyOn(api, 'fetchIssues').mockResolvedValue({
    issues: [], pagination: { page: 1, hasMore: false }, rateLimit: { limited: false },
  });
  vi.spyOn(api, 'fetchPRs').mockResolvedValue({ prs: [link.pr], pagination: { page: 1, hasMore: false }, rateLimit: { limited: false } });
  vi.mocked(prWorkflowApi.links).mockResolvedValue({ pullRequests: [link] });
  render(view());
  await screen.findByRole('link', { name: '#42 Newly created PR' });
  await userEvent.click(screen.getByRole('tab', { name: 'Issues' }));
  await screen.findByText('No issues.');
  expect(fetch).toHaveBeenCalledOnce();
  expect(fetch.mock.calls[0][0].refresh).toBe(false);
});
