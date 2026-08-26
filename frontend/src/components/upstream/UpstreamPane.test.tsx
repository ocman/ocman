// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { UpstreamPane } from './UpstreamPane';
import * as upstreamApi from '../../lib/upstreamApi';
import { useGitInfo } from '../../lib/useGitInfo';
import { _resetForgeUserCacheForTests } from '../../lib/useForgeUser';

const upstreamListMock = vi.hoisted(() => ({ items: [] as unknown[] }));

vi.mock('../../lib/useGitInfo', () => ({
  useGitInfo: vi.fn(() => ({ infos: {}, loading: false, error: null })),
}));
vi.mock('../../lib/useUpstreamList', () => ({
  useUpstreamList: () => ({
    items: upstreamListMock.items, loading: false, error: null, page: 1,
    pagination: { page: 1, hasMore: false }, rateLimit: { limited: false },
    refresh: vi.fn(), setPage: vi.fn(),
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
  vi.spyOn(upstreamApi, 'fetchForgeUser').mockResolvedValue({ login: 'alice', host: 'github.com' });
});

describe('UpstreamPane owner-scoped resources', () => {
  it('uses one git-info poll for every upstream group', () => {
    render(<UpstreamPane directory="/repo" remoteId="box" upstreams={upstreams} />);

    expect(useGitInfo).toHaveBeenCalledTimes(1);
    expect(useGitInfo).toHaveBeenCalledWith(['/repo'], 'box');
  });

  it('does not poll git info without a supported upstream', () => {
    render(<UpstreamPane directory="/repo" remoteId="box" upstreams={[]} />);
    expect(useGitInfo).toHaveBeenCalledWith([], 'box');
  });

  it('reuses forge identities when switching tabs', async () => {
    render(<UpstreamPane directory="/repo" remoteId="box" upstreams={upstreams} />);
    fireEvent.click(screen.getByTestId('upstream-filter-mine'));
    await waitFor(() => expect(upstreamApi.fetchForgeUser).toHaveBeenCalledTimes(2));

    fireEvent.click(screen.getByRole('tab', { name: 'Issues' }));
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Issues' })).toHaveAttribute('aria-selected', 'true'));
    fireEvent.click(screen.getByTestId('upstream-filter-mine'));
    expect(upstreamApi.fetchForgeUser).toHaveBeenCalledTimes(2);
  });

  it('retries unauthenticated forge identities after a tab remount', async () => {
    vi.mocked(upstreamApi.fetchForgeUser).mockResolvedValue(null);
    render(<UpstreamPane directory="/repo" remoteId="box" upstreams={upstreams} />);
    fireEvent.click(screen.getByTestId('upstream-filter-mine'));
    await waitFor(() => expect(upstreamApi.fetchForgeUser).toHaveBeenCalledTimes(2));

    fireEvent.click(screen.getByRole('tab', { name: 'Issues' }));
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

  it('resets row launch state when the project changes', () => {
    upstreamListMock.items = [{
      number: 42, title: 'Patch', body: '', author: 'alice', status: 'open', updatedAt: '',
      labels: [], assignees: [], requestedReviewers: [], branch: 'patch', url: 'https://example/pr/42',
      host: 'github.com', repo: 'a/repo', crossFork: false,
    }];
    const oneUpstream = [upstreams[0]];
    const { rerender } = render(<UpstreamPane directory="/old" remoteId="box" upstreams={oneUpstream} />);
    fireEvent.click(screen.getByRole('button', { expanded: false }));
    fireEvent.click(screen.getByTestId('launch-menu-toggle'));
    expect(screen.getByRole('menu')).toBeInTheDocument();

    rerender(<UpstreamPane directory="/new" remoteId="box" upstreams={oneUpstream} />);
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });
});
