// @vitest-environment jsdom
import { act, cleanup, render, renderHook, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { ProviderPreview } from './ProviderLinkPreview';
import { usePRMergeability } from '../lib/usePRMergeability';
import { cachePRChecks, clearPRChecksCache, prChecksCacheKey } from '../lib/prChecksCache';
import * as api from '../lib/upstreamApi';
import type { PR } from '../lib/upstreamApi';
import * as previews from '../lib/previews';

const preview = { provider: 'github', kind: 'pr', id: 'o/r#42', url: 'https://github.com/o/r/pull/42', title: 'PR', state: 'ok' as const, status: 'Open', headSha: 'abc123', updatedAt: '2026-10-13T00:00:00Z' };
const pr = { host: 'github.com', repo: 'o/r', number: 42, status: 'open', headSha: preview.headSha, updatedAt: preview.updatedAt } as PR;
afterEach(() => { cleanup(); clearPRChecksCache(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it('shows sidebar mergeability and approval without another request', async () => {
  vi.spyOn(api, 'fetchPRMergeability').mockResolvedValue({ mergeable: false, approved: true });
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  cachePRChecks(prChecksCacheKey(pr.host, pr.repo, pr.headSha!), { state: 'success', checks: [{ name: 'build', state: 'success' }] });
  const { result } = renderHook(() => usePRMergeability(pr, '/repo', 'local', 'origin', true));
  await waitFor(() => expect(result.current.approved).toBe(true));
  render(<ProviderPreview preview={preview} providers={[]} />);
  expect(await screen.findByText('Not mergeable')).toBeInTheDocument();
  expect(screen.getByText('Approved')).toBeInTheDocument();
  expect(fetch).not.toHaveBeenCalled();
});

it('also shares mergeability already supplied by the sidebar PR list', async () => {
  vi.spyOn(api, 'fetchPRMergeability').mockResolvedValue({ mergeable: null, approved: true });
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  cachePRChecks(prChecksCacheKey(pr.host, pr.repo, pr.headSha!), { state: 'success', checks: [{ name: 'build', state: 'success' }] });
  const { result } = renderHook(() => usePRMergeability({ ...pr, mergeable: true }, '/repo', 'local', 'origin', true));
  await waitFor(() => expect(result.current.approved).toBe(true));
  render(<ProviderPreview preview={preview} providers={[]} />);
  expect(await screen.findByText('Mergeable')).toBeInTheDocument();
  expect(fetch).not.toHaveBeenCalled();
});

it('shares an in-flight sidebar request with a visible preview', async () => {
  let resolve!: (value: api.PRMergeability) => void;
  vi.spyOn(api, 'fetchPRMergeability').mockImplementation(() => new Promise((done) => { resolve = done; }));
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  cachePRChecks(prChecksCacheKey(pr.host, pr.repo, pr.headSha!), { state: 'success', checks: [{ name: 'build', state: 'success' }] });
  renderHook(() => usePRMergeability(pr, '/repo', 'local', 'origin', true));
  render(<ProviderPreview preview={preview} providers={[]} />);
  await act(async () => resolve({ mergeable: true, approved: false }));
  expect(await screen.findByText('Mergeable')).toBeInTheDocument();
  expect(screen.queryByText('Approved')).toBeNull();
  expect(fetch).not.toHaveBeenCalled();
});

it('fills the sidebar status cache from a preview without needing a head SHA', async () => {
  const load = vi.spyOn(previews, 'fetchPreviewPRStatus').mockResolvedValue({ mergeable: true, approved: true });
  vi.spyOn(api, 'fetchPRMergeability');
  render(<ProviderPreview preview={{ ...preview, headSha: undefined }} providers={[]} />);
  expect(await screen.findByText('Approved')).toBeInTheDocument();
  const { result } = renderHook(() => usePRMergeability({ ...pr, headSha: undefined }, '/sibling-worktree', 'local', 'upstream', true));
  await waitFor(() => expect(result.current).toEqual({ mergeable: true, approved: true }));
  expect(load).toHaveBeenCalledOnce();
  expect(api.fetchPRMergeability).not.toHaveBeenCalled();
});

it.each(['Draft', 'Closed', 'Merged'])('renders %s status without a mergeability claim for settled PRs', async (status) => {
  vi.spyOn(previews, 'fetchPreviewPRStatus').mockResolvedValue({ mergeable: null, approved: true });
  render(<ProviderPreview preview={{ ...preview, status, headSha: undefined }} providers={[]} />);
  expect(await screen.findByText('Approved')).toBeInTheDocument();
  if (status === 'Draft') expect(screen.getByText('Not mergeable')).toBeInTheDocument();
  else expect(screen.queryByText(/^(Not mergeable|Mergeable)$/)).toBeNull();
});

it('does not reuse status from a different owner', async () => {
  cachePRChecks(prChecksCacheKey(pr.host, pr.repo, pr.headSha!), { state: 'success', checks: [{ name: 'build', state: 'success' }] });
  vi.spyOn(api, 'fetchPRMergeability').mockResolvedValue({ mergeable: true, approved: true });
  vi.spyOn(previews, 'fetchPreviewPRStatus').mockResolvedValue({ mergeable: false, approved: false });
  const { result } = renderHook(() => usePRMergeability(pr, '/repo', 'remote-one', 'origin', true));
  await waitFor(() => expect(result.current.approved).toBe(true));
  render(<ProviderPreview preview={preview} providers={[]} />);
  expect(await screen.findByText('Not mergeable')).toBeInTheDocument();
  expect(screen.queryByText('Approved')).toBeNull();
  expect(previews.fetchPreviewPRStatus).toHaveBeenCalledOnce();
});
