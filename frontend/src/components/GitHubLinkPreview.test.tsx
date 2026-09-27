// @vitest-environment jsdom

import { render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi, afterEach, beforeEach } from 'vitest';
import type { PreviewResult } from '../lib/previews';

function jsonResponse(body: unknown, ok = true, status = 200) {
  return { ok, status, json: async () => body } as Response;
}

let providersStatus = 200;
let previews: PreviewResult[] = [];
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.resetModules();
  providersStatus = 200;
  fetchMock = vi.fn().mockImplementation((url: string) => {
    if (url === '/api/settings/link-preview-rules') return Promise.resolve(jsonResponse({ rules: [] }));
    if (url.startsWith('/api/previews/providers')) {
      return Promise.resolve(providersStatus === 200 ? jsonResponse({ providers: [] }) : jsonResponse({}, false, providersStatus));
    }
    if (url.startsWith('/api/previews/resolve')) return Promise.resolve(jsonResponse({ previews }));
    return Promise.resolve(jsonResponse({}, false, 404));
  });
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const resolveCalls = () => fetchMock.mock.calls.filter(([u]) => String(u).startsWith('/api/previews/resolve'));

const pr: PreviewResult = {
  provider: 'github', kind: 'pr', id: 'o/r#1', url: 'https://github.com/o/r/pull/1',
  title: 'Shared PR', status: 'Merged', icon: 'bi-git', meta: ['ann'], state: 'ok',
};

it('renders public forge links as normalized cards without any connected provider', async () => {
  previews = [pr];
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="look at https://github.com/o/r/pull/1 please" />);
  const card = await screen.findByRole('link', { name: /o\/r#1 Shared PR/ });
  expect(card).toHaveAttribute('href', 'https://github.com/o/r/pull/1');
  expect(card).toHaveClass('gh-preview--merged');
  expect(card).toHaveTextContent('Merged · ann');
  // Public links alone never flash a loading status.
  expect(screen.queryByRole('status')).toBeNull();
});

it('still resolves public links when private-preview access is refused', async () => {
  providersStatus = 403;
  previews = [{ ...pr, status: 'Open' }];
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="https://github.com/o/r/pull/1" />);
  expect(await screen.findByRole('link', { name: /Shared PR/ })).toHaveClass('gh-preview--open');
});

it('states the access level on a connect card for a private forge link', async () => {
  fetchMock.mockImplementation((url: string) => {
    if (url.startsWith('/api/previews/providers')) {
      return Promise.resolve(jsonResponse({ providers: [{ id: 'github', name: 'GitHub', notice: 'Read-only access.', connections: [] }] }));
    }
    if (url.startsWith('/api/previews/resolve')) return Promise.resolve(jsonResponse({ previews: [{ ...pr, title: undefined, state: 'connect' }] }));
    return Promise.resolve(jsonResponse({ rules: [] }));
  });
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="https://github.com/o/r/pull/1" />);
  expect(await screen.findByRole('button', { name: 'Connect GitHub for o/r#1' })).toBeInTheDocument();
  expect(screen.getByText('Read-only access.')).toBeInTheDocument();
});

it('skips resolving text without links when nothing is configured', async () => {
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  const { container } = render(<LinkPreviewStrip text="no links here" />);
  await waitFor(() => expect(fetchMock.mock.calls.some(([u]) => String(u).startsWith('/api/previews/providers'))).toBe(true));
  await new Promise((r) => setTimeout(r, 0));
  expect(resolveCalls()).toHaveLength(0);
  expect(container).toBeEmptyDOMElement();
});

it('shows configured text matches as link cards', async () => {
  fetchMock.mockImplementation((url: string) => {
    if (url === '/api/settings/link-preview-rules') return Promise.resolve(jsonResponse({ rules: [
      { pattern: 'ABC-\\d+', replacement: 'https://tracker.example.com/issues/$&' },
    ] }));
    return Promise.resolve(jsonResponse({ providers: [] }));
  });
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="Look at ABC-42 and ABC-42" />);
  expect(await screen.findByRole('link', { name: /ABC-42/ })).toHaveAttribute('href', 'https://tracker.example.com/issues/ABC-42');
  expect(screen.getAllByRole('link')).toHaveLength(1);
});
