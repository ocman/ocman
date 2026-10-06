// @vitest-environment jsdom
import { act, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
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
