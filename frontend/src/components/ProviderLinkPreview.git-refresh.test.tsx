// @vitest-environment jsdom
import { act, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { ProviderPreview } from './ProviderLinkPreview';
import { useProviderPreviews } from '../lib/useProviderPreviews';
import { clearPRChecksCache } from '../lib/prChecksCache';
import { fetchPreviewChecks, resolvePreviews } from '../lib/previews';
import type { PreviewResult } from '../lib/previews';

vi.mock('../lib/previews', async (importOriginal) => ({
  ...await importOriginal<typeof import('../lib/previews')>(),
  loadPreviewConfig: vi.fn().mockResolvedValue(null),
  resolvePreviews: vi.fn(),
  fetchPreviewChecks: vi.fn().mockImplementation((_url: string, sha: string) => Promise.resolve({
    state: sha === 'B' ? 'failure' : 'success', checks: [{ name: 'build', state: sha === 'B' ? 'failure' : 'success' }],
  })),
}));

function Cards({ text = 'https://github.com/a/repo/pull/1' }: { text?: string }) {
  const { previews, providers, refreshChecks } = useProviderPreviews(text);
  return previews.map((preview) => <ProviderPreview key={preview.id} preview={preview} providers={providers} refreshChecks={refreshChecks} />);
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(resolvePreviews).mockReset();
  clearPRChecksCache();
});

it.each(['scoped', 'manual', 'aliased-link'])('keeps an overlapping scoped hint during a %s metadata refresh', async (firstRefresh) => {
  const preview: PreviewResult = { provider: 'github', kind: 'pr', id: 'a/repo#1', url: 'https://github.com/a/repo/pull/1', title: 'Change', state: 'ok', headSha: 'A' };
  let finishOlderRequest!: (previews: PreviewResult[]) => void;
  const olderRequest = new Promise<PreviewResult[]>((resolve) => { finishOlderRequest = resolve; });
  vi.mocked(resolvePreviews)
    .mockResolvedValueOnce([preview])
    .mockImplementationOnce(() => olderRequest)
    .mockResolvedValueOnce([{ ...preview, headSha: 'C' }]);
  render(<Cards text={firstRefresh === 'aliased-link' ? 'https://short.example/pr/1' : undefined} />);
  await screen.findByLabelText('All checks passed');
  act(() => clearPRChecksCache(firstRefresh === 'manual' ? undefined : ['github.com/a/repo']));
  await waitFor(() => expect(resolvePreviews).toHaveBeenCalledTimes(2));
  const olderSignal = vi.mocked(resolvePreviews).mock.calls[1][2]!;
  act(() => clearPRChecksCache(['github.com/a/repo']));
  await act(async () => { finishOlderRequest([{ ...preview, headSha: 'B' }]); });
  await waitFor(() => expect(resolvePreviews).toHaveBeenCalledTimes(3));
  await waitFor(() => expect(fetchPreviewChecks).toHaveBeenCalledWith(preview.url, 'C', 'local', expect.any(AbortSignal), true));
  expect(olderSignal.aborted).toBe(true);
  expect(vi.mocked(fetchPreviewChecks).mock.calls.some(([, sha]) => sha === 'B')).toBe(false);
});

it('keeps a matching hint during the initial metadata lookup', async () => {
  const preview: PreviewResult = { provider: 'github', kind: 'pr', id: 'a/repo#1', url: 'https://github.com/a/repo/pull/1', title: 'Change', state: 'ok', headSha: 'A' };
  let finishInitial!: (previews: PreviewResult[]) => void;
  vi.mocked(resolvePreviews)
    .mockImplementationOnce(() => new Promise((resolve) => { finishInitial = resolve; }))
    .mockResolvedValueOnce([{ ...preview, headSha: 'C' }]);
  render(<Cards />);
  await waitFor(() => expect(resolvePreviews).toHaveBeenCalledOnce());
  const initialSignal = vi.mocked(resolvePreviews).mock.calls[0][2]!;
  act(() => clearPRChecksCache(['github.com/a/repo']));
  await act(async () => { finishInitial([preview]); });
  await waitFor(() => expect(fetchPreviewChecks).toHaveBeenCalledWith(preview.url, 'C', 'local', expect.any(AbortSignal), true));
  expect(initialSignal.aborted).toBe(true);
  expect(vi.mocked(fetchPreviewChecks).mock.calls.some(([, sha]) => sha === 'A')).toBe(false);
});

it('does not reuse matching identity from a previous text while new metadata loads', async () => {
  const preview: PreviewResult = { provider: 'github', kind: 'pr', id: 'a/repo#1', url: 'https://github.com/a/repo/pull/1', title: 'Change', state: 'ok', headSha: 'A' };
  vi.mocked(resolvePreviews).mockResolvedValueOnce([preview]).mockImplementationOnce(() => new Promise(() => {}));
  const { rerender } = render(<Cards />);
  await screen.findByLabelText('All checks passed');
  rerender(<Cards text="https://short.example/other" />);
  await waitFor(() => expect(resolvePreviews).toHaveBeenCalledTimes(2));
  const unrelatedSignal = vi.mocked(resolvePreviews).mock.calls[1][2]!;
  act(() => clearPRChecksCache(['github.com/a/repo']));
  expect(unrelatedSignal.aborted).toBe(false);
  expect(resolvePreviews).toHaveBeenCalledTimes(2);
});

it('reloads matching PR metadata and fetches checks for the new head after a scoped refresh', async () => {
  clearPRChecksCache();
  const preview: PreviewResult = { provider: 'github', kind: 'pr', id: 'a/repo#1', url: 'https://github.com/a/repo/pull/1', title: 'Change', state: 'ok', headSha: 'A' };
  const unrelated = { ...preview, id: 'other/repo#2', title: 'Unrelated', url: 'https://github.com/other/repo/pull/2', headSha: 'C' };
  vi.mocked(resolvePreviews).mockImplementation(async (text, _owner, _signal, refresh) =>
    text.includes('/other/') ? [unrelated] : [{ ...preview, headSha: refresh ? 'B' : 'A' }]);
  render(<><Cards /><Cards text={unrelated.url} /></>);
  await waitFor(() => expect(screen.getAllByLabelText('All checks passed')).toHaveLength(2));
  act(() => clearPRChecksCache(['github.com/a/repo']));
  expect(screen.getByText('other/repo#2 Unrelated')).toBeInTheDocument();
  await screen.findByLabelText('Some checks failed');
  await waitFor(() => expect(fetchPreviewChecks).toHaveBeenCalledWith(preview.url, 'B', 'local', expect.any(AbortSignal), true));
  expect(resolvePreviews).toHaveBeenLastCalledWith('https://github.com/a/repo/pull/1', 'local', expect.any(AbortSignal), true);
  expect(vi.mocked(resolvePreviews).mock.calls.filter(([text]) => text === unrelated.url)).toHaveLength(1);
});
