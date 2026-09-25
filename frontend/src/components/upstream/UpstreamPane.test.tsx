// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { UpstreamPane } from './UpstreamPane';
import * as upstreamApi from '../../lib/upstreamApi';
import { useGitInfo } from '../../lib/useGitInfo';
import { _resetForgeUserCacheForTests } from '../../lib/useForgeUser';

const upstreamListMock = vi.hoisted(() => ({ items: [] as unknown[], page: 1, hasMore: false, setPage: vi.fn() }));

vi.mock('../../lib/useGitInfo', () => ({
  useGitInfo: vi.fn(() => ({ infos: {}, loading: false, error: null })),
}));
vi.mock('../../lib/useUpstreamList', () => ({
  useUpstreamList: () => ({
    items: upstreamListMock.items, loading: false, error: null, page: upstreamListMock.page,
    pagination: { page: upstreamListMock.page, hasMore: upstreamListMock.hasMore }, rateLimit: { limited: false },
    refresh: vi.fn(), setPage: upstreamListMock.setPage,
  }),
}));

const upstreams = [
  { remote: 'origin', host: 'github.com', type: 'github' as const, repo: 'a/repo' },
  { remote: 'mirror', host: 'code.example', type: 'forgejo' as const, repo: 'a/repo' },
];

beforeEach(() => {
  vi.clearAllMocks();
  _resetForgeUserCacheForTests();
  upstreamListMock.items = [];
  upstreamListMock.page = 1;
  upstreamListMock.hasMore = false;
  vi.spyOn(upstreamApi, 'fetchForgeUser').mockResolvedValue({ login: 'alice', host: 'github.com' });
});

describe('UpstreamPane owner-scoped resources', () => {
  it('hides pagination on the only page', () => {
    render(<UpstreamPane directory="/repo" remoteId="box" upstreams={[upstreams[0]]} />);
    expect(screen.queryByRole('group', { name: 'Pagination' })).not.toBeInTheDocument();
  });

  it.each([
    { page: 1, hasMore: true, previousDisabled: true, calls: [2] },
    { page: 2, hasMore: true, previousDisabled: false, calls: [1, 3] },
    { page: 3, hasMore: false, previousDisabled: false, calls: [2] },
  ])('keeps one-based callbacks for page $page with hasMore=$hasMore', ({ page, hasMore, previousDisabled, calls }) => {
    upstreamListMock.page = page;
    upstreamListMock.hasMore = hasMore;
    render(<UpstreamPane directory="/repo" remoteId="box" upstreams={[upstreams[0]]} />);
    expect(screen.getByRole('group', { name: 'Pagination' })).toHaveTextContent(`page ${page}`);
    const previous = screen.getByRole('button', { name: '‹ Prev' });
    const next = screen.getByRole('button', { name: 'Next ›' });
    expect(previous).toHaveAttribute('data-testid', 'upstream-page-prev');
    expect(next).toHaveAttribute('data-testid', 'upstream-page-next');
    expect(previous).toHaveProperty('disabled', previousDisabled);
    expect(next).toHaveProperty('disabled', !hasMore);
    fireEvent.click(previous);
    fireEvent.click(next);
    expect(upstreamListMock.setPage.mock.calls).toEqual(calls.map((value) => [value]));
  });

  it('uses one git-info poll for every upstream group', () => {
    render(<UpstreamPane directory="/repo" remoteId="box" upstreams={upstreams} />);

    expect(useGitInfo).toHaveBeenCalledTimes(1);
    expect(useGitInfo).toHaveBeenCalledWith(['/repo'], 'box');
  });

  it('does not poll git info without a supported upstream', () => {
    render(<UpstreamPane directory="/repo" remoteId="box" upstreams={[]} />);
    expect(useGitInfo).toHaveBeenCalledWith([], 'box');
  });

  it('activates with the keyboard and preserves each tab’s filters across remounts', async () => {
    const user = userEvent.setup();
    render(<UpstreamPane directory="/repo" remoteId="box" upstreams={upstreams} />);
    await user.click(screen.getByTestId('upstream-filter-closed'));
    await user.click(screen.getByRole('tab', { name: 'PRs' }));
    await user.keyboard('{ArrowRight}');
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Issues' })).toHaveFocus());
    expect(screen.getByRole('tabpanel', { name: 'PRs' })).toBeInTheDocument();
    await user.keyboard('{Enter}');
    expect(screen.getAllByRole('tabpanel')).toHaveLength(1);
    expect(screen.getByRole('tabpanel', { name: 'Issues' })).toBeInTheDocument();
    expect(screen.getByTestId('upstream-filter-open')).toHaveClass('active');
    await user.click(screen.getByRole('tab', { name: 'PRs' }));
    expect(screen.getByTestId('upstream-filter-closed')).toHaveClass('active');
  });

  it('reuses forge identities when switching tabs', async () => {
    render(<UpstreamPane directory="/repo" remoteId="box" upstreams={upstreams} />);
    fireEvent.click(screen.getByTestId('upstream-filter-mine'));
    await waitFor(() => expect(upstreamApi.fetchForgeUser).toHaveBeenCalledTimes(2));

    await userEvent.click(screen.getByRole('tab', { name: 'Issues' }));
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Issues' })).toHaveAttribute('aria-selected', 'true'));
    fireEvent.click(screen.getByTestId('upstream-filter-mine'));
    expect(upstreamApi.fetchForgeUser).toHaveBeenCalledTimes(2);
  });

  it('retries unauthenticated forge identities after a tab remount', async () => {
    vi.mocked(upstreamApi.fetchForgeUser).mockResolvedValue(null);
    render(<UpstreamPane directory="/repo" remoteId="box" upstreams={upstreams} />);
    fireEvent.click(screen.getByTestId('upstream-filter-mine'));
    await waitFor(() => expect(upstreamApi.fetchForgeUser).toHaveBeenCalledTimes(2));

    await userEvent.click(screen.getByRole('tab', { name: 'Issues' }));
    fireEvent.click(screen.getByTestId('upstream-filter-mine'));
    await waitFor(() => expect(upstreamApi.fetchForgeUser).toHaveBeenCalledTimes(4));
  });

  it('evicts old successful forge identities', async () => {
    const oneUpstream = [upstreams[0]];
    const { rerender } = render(<UpstreamPane directory="/repo/0" remoteId="box" upstreams={oneUpstream} />);
    fireEvent.click(screen.getByTestId('upstream-filter-mine'));
    await waitFor(() => expect(upstreamApi.fetchForgeUser).toHaveBeenCalledTimes(1));
    for (let i = 1; i <= 32; i += 1) {
      rerender(<UpstreamPane directory={`/repo/${i}`} remoteId="box" upstreams={oneUpstream} />);
      await waitFor(() => expect(upstreamApi.fetchForgeUser).toHaveBeenCalledTimes(i + 1));
    }
    rerender(<UpstreamPane directory="/repo/0" remoteId="box" upstreams={oneUpstream} />);
    await waitFor(() => expect(upstreamApi.fetchForgeUser).toHaveBeenCalledTimes(34));
  });

  it('resets row launch state when the project changes', async () => {
    upstreamListMock.items = [{
      number: 42, title: 'Patch', body: '', author: 'alice', status: 'open', updatedAt: '',
      labels: [], assignees: [], requestedReviewers: [], branch: 'patch', url: 'https://example/pr/42',
      host: 'github.com', repo: 'a/repo', crossFork: false,
    }];
    const oneUpstream = [upstreams[0]];
    const { rerender } = render(<UpstreamPane directory="/old" remoteId="box" upstreams={oneUpstream} />);
    fireEvent.click(screen.getByRole('button', { expanded: false }));
    await userEvent.click(screen.getByTestId('launch-menu-toggle'));
    expect(screen.getByRole('menu')).toBeInTheDocument();

    rerender(<UpstreamPane directory="/new" remoteId="box" upstreams={oneUpstream} />);
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });
});
