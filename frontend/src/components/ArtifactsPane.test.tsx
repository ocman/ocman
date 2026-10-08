// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import type { Artifact } from '../lib/artifactsApi';
import { useUiStore } from '../lib/uiStore';
import { RightPanel } from './RightPanel';

const created: Array<() => void> = [];
vi.mock('../lib/useGlobalEvents', () => ({ onArtifactCreated: (cb: () => void) => { created.push(cb); return () => undefined; } }));
vi.mock('../lib/pluginPanes', async (importOriginal) => ({
  ...await importOriginal<typeof import('../lib/pluginPanes')>(),
  usePluginPanes: () => ({ data: [] }),
}));
vi.mock('../lib/useUpstreams', () => ({ useUpstreams: () => ({ upstreams: [] }) }));

const artifact = (id: string): Artifact => ({
  id, title: `Report ${id}`, directory: '/src/repo', remoteId: 'local', createdAt: '2026-09-01T10:00:00Z',
  items: [{ kind: 'file', name: 'a.txt', mime: 'text/plain', size: 5, url: `/api/artifacts/${id}/files/0` }, { kind: 'link', url: 'https://x.test', label: 'PR' }],
});

let urls: string[];
let pages: Record<string, { artifacts: Artifact[]; nextCursor: string }>;

beforeEach(() => {
  urls = [];
  created.length = 0;
  pages = {};
  vi.stubGlobal('fetch', vi.fn(async (input: string) => {
    urls.push(input);
    if (input.includes('/files/')) return new Response('hello');
    const q = new URL(input, 'http://x').searchParams;
    const page = pages[q.get('cursor') ?? ''] ?? { artifacts: [], nextCursor: '' };
    return new Response(JSON.stringify(page), { headers: { 'content-type': 'application/json' } });
  }));
  useUiStore.persist.setOptions({ storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} } });
  useUiStore.setState({ changesSidebarOpenTabs: ['artifacts'], changesSidebarTabOrder: [], changesSidebarTabSizes: {}, artifactsSidebarScope: 'session' });
});

const renderPanel = () => render(
  <MemoryRouter>
    <RightPanel sessionId="ses 1" platformId="r-a:opencode" directory="/src/.worktrees/repo/feat" messageBookmarkGroups={[]}
      selectedMessageBookmarkKey={null} onRemoveMessageBookmark={vi.fn()} onScrollToMessageBookmark={vi.fn()} />
  </MemoryRouter>,
);

const listParams = () => urls.filter((u) => u.startsWith('/api/artifacts?')).map((u) => Object.fromEntries(new URL(u, 'http://x').searchParams));

it('renders the Artifacts tab and queries this session with descendants', async () => {
  pages[''] = { artifacts: [artifact('a1')], nextCursor: '' };
  renderPanel();
  expect(screen.getByRole('tab', { name: 'Artifacts' })).toHaveAttribute('aria-selected', 'true');
  expect(await screen.findByText('Report a1')).toBeInTheDocument();
  expect(listParams()).toEqual([{ platform: 'r-a:opencode', sessionId: 'ses 1', includeDescendants: '1' }]);
});

it('recovers a resume refresh after an older page stalls until its deadline', async () => {
  let hidden = false;
  const visibility = vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  let heads = 0;
  let paginationSignal: AbortSignal | undefined;
  vi.mocked(fetch).mockImplementation(async (input, options) => {
    const query = new URL(String(input), 'http://x').searchParams;
    if (query.get('cursor')) {
      paginationSignal = options?.signal as AbortSignal;
      return new Promise((_resolve, reject) => paginationSignal?.addEventListener('abort', () => reject(new DOMException('Timed out', 'AbortError')), { once: true }));
    }
    return new Response(JSON.stringify({ artifacts: [artifact(++heads === 1 ? 'first' : 'fresh')], nextCursor: heads === 1 ? 'next' : '' }));
  });
  const view = renderPanel();
  try {
    await waitFor(() => expect(screen.getByRole('button', { name: 'Load more' })).toBeEnabled());
    vi.useFakeTimers();
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await act(async () => {});
    act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
    act(() => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
    await act(async () => { await vi.advanceTimersByTimeAsync(15_001); });
    expect(paginationSignal?.aborted).toBe(true);
    expect(screen.getByText('Report fresh')).toBeInTheDocument();
  } finally { view.unmount(); visibility.mockRestore(); vi.useRealTimers(); }
});

it('switches resource scopes without waiting for obsolete pagination', async () => {
  let oldSignal: AbortSignal | undefined;
  vi.mocked(fetch).mockImplementation(async (input, options) => {
    const query = new URL(String(input), 'http://x').searchParams;
    if (query.get('cursor')) {
      oldSignal = options?.signal as AbortSignal;
      return new Promise(() => {});
    }
    return new Response(JSON.stringify({ artifacts: [artifact(query.get('directory') ? 'project' : 'session')], nextCursor: 'next' }));
  });
  renderPanel();
  await userEvent.click(await screen.findByRole('button', { name: 'Load more' }));
  await userEvent.click(screen.getByRole('radio', { name: 'This project' }));
  expect(await screen.findByText('Report project')).toBeInTheDocument();
  expect(oldSignal?.aborted).toBe(true);
});

it('keeps loaded pages and an expanded preview across document hide/resume', async () => {
  let hidden = false;
  const visibility = vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  pages[''] = { artifacts: [artifact('a1')], nextCursor: 'c2' };
  pages.c2 = { artifacts: [artifact('a2')], nextCursor: '' };
  const view = renderPanel();
  try {
    await userEvent.click(await screen.findByRole('button', { name: 'Load more' }));
    await userEvent.click(await screen.findByRole('button', { name: /Report a2/ }));
    const preview = await screen.findByTestId('artifact-preview-text');
    const before = listParams().length;
    act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
    expect(listParams()).toHaveLength(before);
    expect(screen.getByTestId('artifact-preview-text')).toBe(preview);
    act(() => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
    await waitFor(() => expect(listParams()).toHaveLength(before + 2));
    expect(screen.getByTestId('artifact-preview-text')).toBe(preview);
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument();
  } finally { view.unmount(); visibility.mockRestore(); }
});

it('keeps later pages and their expanded previews when a failed resume is retried', async () => {
  let hidden = false;
  const visibility = vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  pages[''] = { artifacts: [artifact('a1')], nextCursor: 'c2' };
  pages.c2 = { artifacts: [artifact('a2')], nextCursor: '' };
  const view = renderPanel();
  try {
    await userEvent.click(await screen.findByRole('button', { name: 'Load more' }));
    await userEvent.click(await screen.findByRole('button', { name: /Report a2/ }));
    const preview = await screen.findByTestId('artifact-preview-text');
    vi.mocked(fetch).mockRejectedValueOnce(new Error('resume unavailable'));
    act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
    act(() => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
    await screen.findByText(/resume unavailable/);
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.queryByText(/resume unavailable/)).not.toBeInTheDocument());
    expect(screen.getByText('Report a2')).toBeInTheDocument();
    expect(screen.getByTestId('artifact-preview-text')).toBe(preview);
  } finally { view.unmount(); visibility.mockRestore(); }
});

it('includes pagination already pending at hide time in the resume snapshot', async () => {
  let hidden = false;
  const visibility = vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  let finishMore!: (response: Response) => void;
  let finishResume!: (response: Response) => void;
  const more = new Promise<Response>((resolve) => { finishMore = resolve; });
  const resume = new Promise<Response>((resolve) => { finishResume = resolve; });
  let heads = 0, older = 0;
  const response = (items: Artifact[], nextCursor = '') => new Response(JSON.stringify({ artifacts: items, nextCursor }));
  vi.mocked(fetch).mockImplementation(async (input) => {
    urls.push(String(input));
    const cursor = new URL(String(input), 'http://x').searchParams.get('cursor');
    if (!cursor) return ++heads === 1 ? response([artifact('a1')], 'c2') : resume;
    return ++older === 1 ? more : response([artifact('a2')]);
  });
  const view = renderPanel();
  try {
    await userEvent.click(await screen.findByRole('button', { name: 'Load more' }));
    act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
    act(() => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
    await act(async () => { finishMore(response([artifact('a2')])); });
    await act(async () => { finishResume(response([artifact('a1')], 'c2')); });
    await waitFor(() => expect(screen.getByText('Report a2')).toBeInTheDocument());
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument();
  } finally { view.unmount(); visibility.mockRestore(); }
});

it.each(['refresh-first', 'more-first'] as const)('uses the refreshed cursor when pagination overlaps resume: %s', async (order) => {
  let hidden = false;
  const visibility = vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  let finishHead!: (response: Response) => void;
  let finishOldCursor!: (response: Response) => void;
  const head = new Promise<Response>((resolve) => { finishHead = resolve; });
  const oldCursor = new Promise<Response>((resolve) => { finishOldCursor = resolve; });
  const response = (items: Artifact[], nextCursor = '') => new Response(JSON.stringify({ artifacts: items, nextCursor }));
  let heads = 0;
  vi.mocked(fetch).mockImplementation(async (input) => {
    urls.push(String(input));
    const cursor = new URL(String(input), 'http://x').searchParams.get('cursor');
    if (!cursor) return ++heads === 1 ? response([artifact('a1')], 'old-cursor') : head;
    return cursor === 'old-cursor' ? oldCursor : response([artifact('correct-next')]);
  });
  const view = renderPanel();
  try {
    await screen.findByText('Report a1');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Load more' })).toBeEnabled());
    await userEvent.click(screen.getByRole('button', { name: 'Load more' }));
    act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
    act(() => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
    const finishRefresh = async () => { await act(async () => { finishHead(response([artifact('fresh')], 'fresh-cursor')); }); };
    const finishMore = async () => { await act(async () => { finishOldCursor(response([artifact('stale-next')])); }); };
    if (order === 'refresh-first') { await finishRefresh(); await finishMore(); }
    else { await finishMore(); await finishRefresh(); }
    expect(await screen.findByText('Report correct-next')).toBeInTheDocument();
    expect(screen.queryByText('Report stale-next')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument();
  } finally { view.unmount(); visibility.mockRestore(); }
});

it('shows loading before an empty result and retries a failed owner-scoped read', async () => {
  let resolve!: (response: Response) => void;
  vi.mocked(fetch).mockImplementationOnce(() => new Promise<Response>(done => { resolve = done; }));
  renderPanel();
  expect(within(screen.getByTestId('artifacts-pane')).getByRole('status')).toHaveTextContent('Loading artifacts');
  expect(screen.queryByText('No artifacts yet.')).not.toBeInTheDocument();
  await waitFor(() => expect(resolve).toBeTypeOf('function'));
  await act(async () => resolve(new Response('Could not read artifacts', { status: 503 })));
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not read artifacts');
  pages[''] = { artifacts: [artifact('recovered')], nextCursor: '' };
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
  expect(await screen.findByText('Report recovered')).toBeVisible();
  expect(listParams().at(-1)).toEqual({ platform: 'r-a:opencode', sessionId: 'ses 1', includeDescendants: '1' });
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('switches to the project sub-tab, persists it, and folds worktrees to the project root', async () => {
  renderPanel();
  await userEvent.click(screen.getByRole('radio', { name: 'This project' }));
  expect(useUiStore.getState().artifactsSidebarScope).toBe('project');
  await waitFor(() => expect(listParams().at(-1)).toEqual({ directory: '/src/repo' }));
});

it('loads more, refreshes on artifact.created, and expands rows to previews', async () => {
  pages[''] = { artifacts: [artifact('a1')], nextCursor: 'c2' };
  pages.c2 = { artifacts: [artifact('a2')], nextCursor: '' };
  renderPanel();
  await userEvent.click(await screen.findByRole('button', { name: 'Load more' }));
  expect(await screen.findByText('Report a2')).toBeInTheDocument();
  expect(listParams().at(-1)).toMatchObject({ cursor: 'c2', includeDescendants: '1' });
  expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument();

  const before = listParams().length;
  act(() => created.forEach((cb) => cb()));
  await waitFor(() => expect(listParams().length).toBe(before + 1));
  await waitFor(() => expect(screen.queryByText('Report a2')).not.toBeInTheDocument());

  await userEvent.click(screen.getByRole('button', { name: /Report a1/ }));
  expect(screen.getByRole('link', { name: 'Open artifact' })).toHaveAttribute('href', '/artifacts/a1');
  expect(screen.getByRole('link', { name: 'PR' })).toHaveAttribute('href', 'https://x.test');
  expect(await screen.findByTestId('artifact-preview-text')).toHaveTextContent('hello');
});
