// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { UpstreamRemoteGroup } from './UpstreamRemoteGroup';
import * as api from '../../lib/upstreamApi';
import { clearPRChecksCache } from '../../lib/prChecksCache';

afterEach(() => { vi.restoreAllMocks(); clearPRChecksCache(); });

it('keeps an expanded PR row and its CI status mounted through list and checks refresh', async () => {
  clearPRChecksCache();
  const pr: api.PR = {
    number: 7, title: 'Change', body: 'Details', author: 'alice', status: 'open',
    updatedAt: '2026-10-06T00:00:00Z', labels: [], assignees: [], requestedReviewers: [],
    branch: 'fix', headSha: 'abc123', url: 'https://github.com/a/repo/pull/7',
    host: 'github.com', repo: 'a/repo', crossFork: false,
  };
  const initial = { prs: [pr], pagination: { page: 1, hasMore: true }, rateLimit: { limited: false } };
  let finishList!: (response: typeof initial) => void;
  let finishChecks!: (response: api.PRChecks) => void;
  vi.spyOn(api, 'fetchPRs').mockResolvedValueOnce(initial)
    .mockImplementationOnce(() => new Promise((resolve) => { finishList = resolve; }));
  vi.spyOn(api, 'fetchPRChecks').mockResolvedValueOnce({ state: 'success', checks: [{ name: 'build', state: 'success' }] })
    .mockImplementationOnce(() => new Promise((resolve) => { finishChecks = resolve; }));
  let refresh!: () => void;
  render(<UpstreamRemoteGroup kind="prs" directory="/repo" launchDirectory="/repo" remoteId="local"
    upstream={{ remote: 'origin', host: 'github.com', type: 'github', repo: 'a/repo' }}
    state="open" mine={false} showHeader={false} onLoadingChange={() => {}}
    registerRefresh={(fn) => { refresh = fn; return () => {}; }} />);
  const row = await screen.findByTestId('pr-row-7');
  const badge = await screen.findByRole('img', { name: 'All checks passed' });
  fireEvent.click(screen.getByRole('button', { expanded: false }));
  const details = screen.getByTestId('pr-detail-7');
  act(() => { clearPRChecksCache(); refresh(); });
  await waitFor(() => expect(api.fetchPRChecks).toHaveBeenCalledTimes(2));
  expect(screen.getByTestId('pr-row-7')).toBe(row);
  expect(screen.getByRole('img', { name: 'All checks passed' })).toBe(badge);
  expect(screen.getByTestId('pr-detail-7')).toBe(details);
  expect(screen.getByRole('button', { name: 'Next ›' })).toBeEnabled();
  await act(async () => finishList({ ...initial, prs: [{ ...pr, title: 'Updated change' }] }));
  expect(screen.getByTestId('pr-row-7')).toBe(row);
  expect(screen.getByTestId('pr-detail-7')).toBe(details);
  expect(screen.getByRole('img', { name: 'All checks passed' })).toBe(badge);
  expect(screen.getByText('Updated change')).toBeInTheDocument();
  await act(async () => finishChecks({ state: 'failure', checks: [{ name: 'build', state: 'failure' }] }));
  expect(screen.getByRole('img', { name: 'Some checks failed' })).toBe(badge);
});
