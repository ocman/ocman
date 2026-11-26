// @vitest-environment jsdom

import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi, afterEach, beforeEach } from 'vitest';
import type { PreviewResult } from '../lib/previews';

function jsonResponse(body: unknown, ok = true, status = 200) {
  return { ok, status, json: async () => body } as Response;
}

const github = { id: 'github', name: 'GitHub', hosts: ['github.com'], configured: true, source: 'public', token: true, oauth: false, accounts: [] };
let providersStatus = 200;
let previews: PreviewResult[] = [];
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.resetModules();
  providersStatus = 200;
  fetchMock = vi.fn().mockImplementation((url: string) => {
    if (url === '/api/settings/link-preview-rules') return Promise.resolve(jsonResponse({ rules: [] }));
    if (url.startsWith('/api/previews/providers')) {
      return Promise.resolve(providersStatus === 200 ? jsonResponse({ providers: [github], rules: [] }) : jsonResponse({}, false, providersStatus));
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
  expect(screen.queryByRole('status')).toBeNull();
});

it('updates older cards when a later message resolves the same PR as merged', async () => {
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  const conversation = (later: boolean) => <>
    <LinkPreviewStrip text={`CI is pending on ${pr.url}`} />
    {later && <LinkPreviewStrip text={`${pr.url} has now merged`} />}
  </>;
  previews = [{ ...pr, status: 'Open' }];
  const { rerender } = render(conversation(false));
  const first = await screen.findByRole('link', { name: /Shared PR/ });
  expect(first).toHaveTextContent('Open');

  previews = [pr];
  rerender(conversation(true));
  await waitFor(() => expect(screen.getAllByText('Merged', { exact: true })).toHaveLength(2));
  expect(first).toHaveTextContent('Merged');
});

it('refreshes a merged preview on demand without refreshing other PRs', async () => {
  const other = { ...pr, id: 'o/r#2', title: 'Other PR', url: 'https://github.com/o/r/pull/2' };
  const originalFetch = fetchMock.getMockImplementation() as (url: string) => Promise<Response>;
  fetchMock.mockImplementation((url: string, init: { body: string }) => {
    if (!url.startsWith('/api/previews/resolve')) return originalFetch(url);
    const body = JSON.parse(init.body);
    const resolve = (req: typeof body) => ({ previews: req.checksSha
      ? [{ state: 'ok', checks: { state: 'success', checks: [] } }]
      : req.prStatus ? [{ state: 'ok', prStatus: { mergeable: null, approved: true } }]
      : req.render ? [pr, other] : [{ ...pr, title: 'Refreshed PR', headSha: 'abc123' }] });
    return Promise.resolve(jsonResponse(body.requests ? { results: body.requests.map(resolve) } : resolve(body)));
  });
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text={`${pr.url} ${other.url}`} />);
  const [refresh] = await screen.findAllByRole('button', { name: 'Refresh preview' });
  expect(resolveCalls()).toHaveLength(1);
  fireEvent.click(refresh);
  expect(await screen.findByRole('link', { name: /Refreshed PR/ })).toBeInTheDocument();
  expect(await screen.findByLabelText('All checks passed')).toBeInTheDocument();
  expect(screen.getByRole('link', { name: /Other PR/ })).toBeInTheDocument();
  const requests = resolveCalls().slice(1).flatMap(([, { body }]) => JSON.parse(body).requests ?? [JSON.parse(body)]);
  expect(requests).toContainEqual({ text: pr.url, refresh: true });
  expect(requests.every((request) => request.text === pr.url)).toBe(true);
});

it('reserves one card per distinct previewable link until the results arrive', async () => {
  const second = { ...pr, id: 'o/r#2', title: 'Second PR', url: 'https://github.com/o/r/pull/2' };
  const originalFetch = fetchMock.getMockImplementation() as (url: string) => Promise<Response>;
  let finish!: (response: Response) => void;
  const pending = new Promise<Response>((resolve) => { finish = resolve; });
  fetchMock.mockImplementation((url: string) => url.startsWith('/api/previews/resolve') ? pending : originalFetch(url));
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text={`${pr.url} ${second.url} ${pr.url} https://example.com/article`} />);

  expect(await screen.findAllByTestId('link-preview-skeleton')).toHaveLength(2);
  expect(screen.queryByRole('link')).toBeNull();
  await act(async () => finish(jsonResponse({ previews: [pr, second] })));
  expect(await screen.findByRole('link', { name: /Second PR/ })).toBeInTheDocument();
  expect(screen.getAllByRole('link')).toHaveLength(2);
  expect(screen.queryByTestId('link-preview-skeleton')).toBeNull();
});

it('removes the placeholder when a candidate produces no preview', async () => {
  const originalFetch = fetchMock.getMockImplementation() as (url: string) => Promise<Response>;
  let finish!: (response: Response) => void;
  const pending = new Promise<Response>((resolve) => { finish = resolve; });
  fetchMock.mockImplementation((url: string) => url.startsWith('/api/previews/resolve') ? pending : originalFetch(url));
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="https://github.com/o/r" />);

  expect(await screen.findByTestId('link-preview-skeleton')).toBeInTheDocument();
  await act(async () => finish(jsonResponse({ previews: [] })));
  await waitFor(() => expect(screen.queryByTestId('gh-preview-strip')).toBeNull());
});

it('still resolves public links when private-preview access is refused', async () => {
  providersStatus = 403;
  previews = [{ ...pr, status: 'Open' }];
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="https://github.com/o/r/pull/1" />);
  expect(await screen.findByRole('link', { name: /Shared PR/ })).toHaveClass('gh-preview--open');
});

it('explains how to preview a private forge link, without a connect flow', async () => {
  previews = [{ ...pr, title: undefined, state: 'connect' }];
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="https://github.com/o/r/pull/1" />);
  expect(await screen.findByText(/GitHub: Private. Add a token under Settings/)).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'o/r#1' })).toHaveAttribute('href', pr.url);
  expect(screen.queryByRole('button')).toBeNull();
});

it('never sends links on hosts without a configured provider', async () => {
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="https://gitlab.example.com/a/b/-/issues/1 https://linear.app/x/issue/ENG-1" />);
  await waitFor(() => expect(fetchMock.mock.calls.some(([u]) => String(u).startsWith('/api/previews/providers'))).toBe(true));
  await new Promise((r) => setTimeout(r, 0));
  expect(resolveCalls()).toHaveLength(0);
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

it('refreshes the PR head before fetching fresh checks for the new commit', async () => {
  let head = 'abc123';
  const originalFetch = fetchMock.getMockImplementation() as (url: string) => Promise<Response>;
  fetchMock.mockImplementation((url: string, init?: { body?: string }) => {
    if (!url.startsWith('/api/previews/resolve')) return originalFetch(url);
    const body = JSON.parse(init?.body ?? '{}');
    const resolve = (one: typeof body) => {
      if (one.checksSha) {
        const state = one.checksSha === 'def456' && one.refreshChecks ? 'failure' : 'success';
        return { previews: [{ state: 'ok', checks: { state, checks: [{ name: 'build', state }] } }] };
      }
      if (one.prStatus) return { previews: [{ state: 'ok', prStatus: { mergeable: true, approved: false } }] };
      return { previews: [{ ...pr, status: 'Open', headSha: one.refresh ? head : 'abc123' }] };
    };
    return Promise.resolve(jsonResponse(body.requests ? { results: body.requests.map(resolve) } : resolve(body)));
  });
  const { clearPRChecksCache } = await import('../lib/prChecksCache');
  clearPRChecksCache();
  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text="https://github.com/o/r/pull/1" />);
  await screen.findByLabelText('All checks passed');
  head = 'def456';
  await act(async () => clearPRChecksCache());
  expect(await screen.findByLabelText('Some checks failed')).toBeInTheDocument();
  expect(resolveCalls().some(([, init]) => {
    const body = JSON.parse(init.body);
    return (body.requests ?? [body]).some((one: typeof body) => one.checksSha === 'def456' && one.refreshChecks === true);
  })).toBe(true);
});
