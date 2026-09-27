// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { PreviewResult } from '../lib/previews';

function json(body: unknown, status = 200) {
  return { ok: status < 400, status, json: async () => body } as Response;
}

const provider = { id: 'mock', name: 'Tracker', connections: [
  { workspaceId: 'w1', workspaceName: 'Acme', accountName: 'alice', sites: [], state: 'connected' },
  { workspaceId: 'w2', workspaceName: 'Beta', accountName: 'alice', sites: [], state: 'connected' },
] };
const rule = { pattern: 'ABC-\\d+', replacement: 'https://tracker.example.com/browse/$&', provider: 'mock' };
let previews: PreviewResult[];
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.resetModules();
  fetchMock = vi.fn().mockImplementation((url: string) => {
    if (url === '/api/settings/link-preview-rules') return Promise.resolve(json({ rules: [rule] }));
    if (url.startsWith('/api/previews/providers')) return Promise.resolve(json({ providers: [provider] }));
    if (url.startsWith('/api/previews/resolve')) return Promise.resolve(json({ previews }));
    if (url.startsWith('/api/previews/connect')) return Promise.resolve(json({ authorizeUrl: 'https://auth.example.com/authorize' }));
    if (url.startsWith('/api/previews/disconnect')) return Promise.resolve(json(undefined, 204));
    return Promise.resolve(json({ forgejo: { available: false, hosts: [] } }));
  });
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const ref = { provider: 'mock', kind: 'issue', id: 'ABC-1', url: 'https://tracker.example.com/browse/ABC-1' };
const resolveBodies = () => fetchMock.mock.calls
  .filter(([u]) => String(u).startsWith('/api/previews/resolve'))
  .map(([, init]) => JSON.parse(String((init as RequestInit).body)));

it('renders a rich card in place of the rule fallback and drops it after a viewer change', async () => {
  previews = [{ ...ref, workspace: 'w1', title: 'Private title', status: 'In progress', state: 'ok' }];
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  const { previewAuthChanged } = await import('../lib/previews');
  render(<LinkPreviewStrip text="Fix ABC-1" />);
  const card = await screen.findByRole('link', { name: /ABC-1 Private title/ });
  expect(card).toHaveAttribute('href', ref.url);
  expect(screen.getAllByRole('link')).toHaveLength(1);
  expect(JSON.stringify({ ...localStorage })).not.toContain('Private');

  previews = [{ ...ref, state: 'connect' }];
  act(() => previewAuthChanged());
  expect(screen.queryByText(/Private title/)).toBeNull();
  expect(await screen.findByRole('button', { name: 'Connect Tracker for ABC-1' })).toBeInTheDocument();
  expect(screen.queryByText(/Private title/)).toBeNull();
  // The custom rule's plain card is back as the fallback.
  expect(screen.getAllByRole('link', { name: /ABC-1/ }).length).toBeGreaterThan(0);
});

it('starts consent from a connect card and offers reconnect when expired or denied', async () => {
  previews = [{ ...ref, state: 'connect' }, { ...ref, id: 'ABC-2', url: undefined, state: 'expired' }, { ...ref, id: 'ABC-3', state: 'denied' }];
  const assign = vi.fn();
  vi.stubGlobal('location', { ...window.location, pathname: '/session/s1', search: '', assign });
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="ABC-1 ABC-2 ABC-3" />);
  expect(await screen.findByRole('button', { name: 'Reconnect Tracker for ABC-2' })).toBeInTheDocument();
  expect(screen.getByText('Tracker: Your account cannot view this')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Connect Tracker for ABC-1' }));
  await waitFor(() => expect(assign).toHaveBeenCalledWith('https://auth.example.com/authorize'));
  const call = fetchMock.mock.calls.find(([u]) => String(u).startsWith('/api/previews/connect'))!;
  expect(JSON.parse(String((call[1] as RequestInit).body))).toEqual({ provider: 'mock', returnTo: '/session/s1' });
});

it('asks for a workspace when ambiguous and resolves with the choice', async () => {
  previews = [{ ...ref, state: 'ambiguous' }];
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="ABC-1" />);
  const select = await screen.findByRole('combobox', { name: 'Workspace for ABC-1' });
  previews = [{ ...ref, workspace: 'w2', title: 'Beta issue', state: 'ok' }];
  fireEvent.change(select, { target: { value: 'w2' } });
  expect(await screen.findByRole('link', { name: /Beta issue/ })).toBeInTheDocument();
  expect(resolveBodies().at(-1)).toEqual({ text: 'ABC-1', workspaces: { mock: 'w2' } });
});

it('shows a plain link for a direct URL the provider cannot resolve', async () => {
  previews = [{ ...ref, id: 'XYZ-8', url: 'https://tracker.example.com/browse/XYZ-8', state: 'error' }];
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="see https://tracker.example.com/browse/XYZ-8" />);
  expect(await screen.findByRole('link', { name: 'XYZ-8' })).toHaveAttribute('href', 'https://tracker.example.com/browse/XYZ-8');
});

it('falls back to plain links when resolving fails or the resource is missing', async () => {
  previews = [{ ...ref, id: 'ABC-9', url: 'https://tracker.example.com/browse/ABC-9', state: 'not_found' }];
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="ABC-9" />);
  await waitFor(() => expect(resolveBodies()).toHaveLength(1));
  expect(screen.getAllByRole('link', { name: /ABC-9/ })).toHaveLength(1);
  expect(screen.queryByRole('button')).toBeNull();
  expect(screen.queryByTestId('provider-preview-card')).toBeNull();
});
