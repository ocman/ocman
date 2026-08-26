// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { UpstreamPane } from './UpstreamPane';
import * as upstreamApi from '../../lib/upstreamApi';
import { useGitInfo } from '../../lib/useGitInfo';
import { _resetForgeUserCacheForTests } from '../../lib/useForgeUser';

vi.mock('../../lib/useGitInfo', () => ({
  useGitInfo: vi.fn(() => ({ infos: {}, loading: false, error: null })),
}));
vi.mock('../../lib/useUpstreamList', () => ({
  useUpstreamList: () => ({
    items: [], loading: false, error: null, page: 1,
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
  vi.spyOn(upstreamApi, 'fetchForgeUser').mockResolvedValue({ login: 'alice', host: 'github.com' });
});

describe('UpstreamPane owner-scoped resources', () => {
  it('uses one git-info poll for every upstream group', () => {
    render(<UpstreamPane directory="/repo" remoteId="box" upstreams={upstreams} />);

    expect(useGitInfo).toHaveBeenCalledTimes(1);
    expect(useGitInfo).toHaveBeenCalledWith(['/repo'], 'box');
  });

  it('reuses forge identities when switching tabs', async () => {
    render(<UpstreamPane directory="/repo" remoteId="box" upstreams={upstreams} />);
    await waitFor(() => expect(upstreamApi.fetchForgeUser).toHaveBeenCalledTimes(2));

    fireEvent.click(screen.getByRole('tab', { name: 'Issues' }));
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Issues' })).toHaveAttribute('aria-selected', 'true'));
    expect(upstreamApi.fetchForgeUser).toHaveBeenCalledTimes(2);
  });

  it('retries unauthenticated forge identities after a tab remount', async () => {
    vi.mocked(upstreamApi.fetchForgeUser).mockResolvedValue(null);
    render(<UpstreamPane directory="/repo" remoteId="box" upstreams={upstreams} />);
    await waitFor(() => expect(upstreamApi.fetchForgeUser).toHaveBeenCalledTimes(2));

    fireEvent.click(screen.getByRole('tab', { name: 'Issues' }));
    await waitFor(() => expect(upstreamApi.fetchForgeUser).toHaveBeenCalledTimes(4));
  });
});
