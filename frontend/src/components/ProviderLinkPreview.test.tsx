// @vitest-environment jsdom
import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { PreviewResult } from '../lib/previews';

function json(body: unknown, status = 200) {
  return { ok: status < 400, status, json: async () => body } as Response;
}

const provider = {
  id: 'mock', name: 'Tracker', hosts: ['tracker.example.com'], configured: true, token: true, oauth: false,
  accounts: [{ workspaceId: 'w1', workspaceName: 'Acme', accountName: 'alice', sites: [], state: 'connected' }],
};
const rule = { pattern: 'ABC-\\d+', replacement: 'https://tracker.example.com/browse/$&', provider: 'mock' };
let previews: PreviewResult[];
let config: unknown;
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.resetModules();
  config = { providers: [provider], rules: [rule.pattern], hostKinds: [] };
  fetchMock = vi.fn().mockImplementation((url: string) => {
    if (url === '/api/settings/link-preview-rules') return Promise.resolve(json({ rules: [rule] }));
    if (url.startsWith('/api/previews/providers')) return Promise.resolve(json(config));
    if (url.startsWith('/api/previews/resolve')) return Promise.resolve(json({ previews }));
    return Promise.resolve(json({ forgejo: { available: false, hosts: [] } }));
  });
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

it.each(['Open', 'Merged', 'Closed'])('colors the %s PR status independently of its metadata', async (status) => {
  const { ProviderPreview } = await import('./ProviderLinkPreview');
  render(<ProviderPreview providers={[]} preview={{
    provider: 'github', kind: 'pr', id: 'owner/repo#1',
    url: 'https://github.com/owner/repo/pull/1', title: 'Fix',
    state: 'ok', status, meta: ['alice'],
  }} />);
  expect(screen.getByText(status)).toHaveClass(`gh-preview__state--${status.toLowerCase()}`);
  expect(screen.getByTestId('provider-preview-card')).toHaveClass(`gh-preview--${status.toLowerCase()}`);
  expect(screen.getByText(/alice/)).toBeInTheDocument();
});

it.each([
  { status: 'Open', meta: [], text: 'Open' },
  { status: 'Open', meta: ['', 'alice'], text: 'Open · alice' },
  { status: 'In progress', meta: ['alice'], text: 'In progress · alice' },
  { status: undefined, meta: ['alice'], text: 'alice' },
])('preserves metadata separators for $text', async ({ status, meta, text }) => {
  const { ProviderPreview } = await import('./ProviderLinkPreview');
  render(<ProviderPreview providers={[]} preview={{ ...ref, title: 'Fix', state: 'ok', status, meta }} />);
  expect(screen.getByTestId('provider-preview-card').querySelector('.gh-preview__meta')).toHaveTextContent(text);
  if (status !== 'Open') expect(screen.queryByText(status ?? 'alice', { selector: '.gh-preview__state' })).toBeNull();
});

const ref = { provider: 'mock', kind: 'issue', id: 'ABC-1', url: 'https://tracker.example.com/browse/ABC-1' };
const resolveBodies = () => fetchMock.mock.calls
  .filter(([u]) => String(u).startsWith('/api/previews/resolve'))
  .map(([, init]) => JSON.parse(String((init as RequestInit).body)));

it('renders a rich card in place of the rule fallback and reloads after a token change', async () => {
  previews = [{ ...ref, workspace: 'w1', title: 'Private title', status: 'In progress', state: 'ok' }];
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  const { previewAuthChanged } = await import('../lib/previews');
  render(<LinkPreviewStrip text="Fix ABC-1" />);
  const card = await screen.findByRole('link', { name: /ABC-1 Private title/ });
  expect(card).toHaveAttribute('href', ref.url);
  expect(screen.getAllByRole('link')).toHaveLength(1);
  expect(resolveBodies()).toEqual([{ text: 'Fix ABC-1' }]);

  previews = [{ ...ref, state: 'connect' }];
  act(() => previewAuthChanged());
  expect(await screen.findByText(/Tracker: Private. Add a token/)).toBeInTheDocument();
  expect(screen.queryByText(/Private title/)).toBeNull();
  // The custom rule's plain card is back as the fallback.
  expect(screen.getAllByRole('link', { name: /ABC-1/ }).length).toBeGreaterThan(0);
});

it('explains expired and denied tokens without any connect flow', async () => {
  previews = [{ ...ref, id: 'ABC-2', url: undefined, state: 'expired' }, { ...ref, id: 'ABC-3', state: 'denied' }];
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="ABC-2 ABC-3" />);
  expect(await screen.findByText(/Tracker: The saved token expired/)).toBeInTheDocument();
  expect(screen.getByText('Tracker: The saved token cannot view this.')).toBeInTheDocument();
  expect(screen.queryByRole('button')).toBeNull();
  expect(screen.queryByRole('combobox')).toBeNull();
});

it('only resolves text that a configured provider or routed rule can preview', async () => {
  previews = [];
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  const { rerender } = render(<LinkPreviewStrip text="https://elsewhere.example.com/browse/ABC" />);
  await new Promise((r) => setTimeout(r, 400));
  expect(resolveBodies()).toHaveLength(0);
  rerender(<LinkPreviewStrip text="see https://tracker.example.com/browse/X" />);
  await waitFor(() => expect(resolveBodies()).toHaveLength(1));

  config = { providers: [{ ...provider, configured: false }], rules: [], hostKinds: [] };
  const { previewAuthChanged } = await import('../lib/previews');
  act(() => previewAuthChanged());
  rerender(<LinkPreviewStrip text="ABC-5 https://tracker.example.com/browse/ABC-5" />);
  await new Promise((r) => setTimeout(r, 400));
  expect(resolveBodies()).toHaveLength(1);
});

it('shows a plain link for a direct URL the provider cannot resolve', async () => {
  previews = [{ ...ref, id: 'XYZ-8', url: 'https://tracker.example.com/browse/XYZ-8', state: 'error' }];
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="see https://tracker.example.com/browse/XYZ-8" />);
  expect(await screen.findByRole('link', { name: 'XYZ-8' })).toHaveAttribute('href', 'https://tracker.example.com/browse/XYZ-8');
});

it('falls back to plain links when the resource is missing or ambiguous', async () => {
  previews = [{ ...ref, id: 'ABC-9', url: 'https://tracker.example.com/browse/ABC-9', state: 'not_found' }, { ...ref, id: 'ABC-8', url: undefined, state: 'ambiguous' }];
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="ABC-9 ABC-8" />);
  await waitFor(() => expect(resolveBodies()).toHaveLength(1));
  expect(screen.getAllByRole('link', { name: /ABC-9/ })).toHaveLength(1);
  expect(screen.queryByRole('button')).toBeNull();
  expect(screen.queryByRole('combobox')).toBeNull();
  expect(screen.queryByTestId('provider-preview-card')).toBeNull();
});

it('offers the matching pages for an ambiguous ticket and labels pages by title', async () => {
  config = { providers: [{ ...provider, hosts: ['www.notion.so'] }], rules: [rule.pattern], hostKinds: [] };
  previews = [
    { ...ref, id: 'ABC-7', url: undefined, state: 'ambiguous', choices: [
      { title: 'ABC-7 Retro', url: 'https://www.notion.so/Retro-1' },
      { title: 'ABC-7 follow-up', url: 'https://www.notion.so/Follow-2' },
    ] },
    { provider: 'mock', kind: 'page', id: '0123-uuid', url: 'https://www.notion.so/Plan-3', title: 'Launch plan', state: 'ok' },
  ];
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="ABC-7 https://www.notion.so/Plan-3" />);
  expect(await screen.findByText('Several pages match:')).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'ABC-7 follow-up' })).toHaveAttribute('href', 'https://www.notion.so/Follow-2');
  expect(screen.getByRole('link', { name: 'Launch plan' })).toHaveAttribute('href', 'https://www.notion.so/Plan-3');
});
