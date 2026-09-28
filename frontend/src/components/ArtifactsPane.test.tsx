// @vitest-environment jsdom
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import type { Artifact } from '../lib/artifactsApi';
import { useUiStore } from '../lib/uiStore';
import { RightPanel } from './RightPanel';

const created: Array<() => void> = [];
vi.mock('../lib/useGlobalEvents', () => ({ onArtifactCreated: (cb: () => void) => { created.push(cb); return () => undefined; } }));
vi.mock('../lib/useBeadsStatus', () => ({ useBeadsStatus: () => ({ data: undefined, error: null, isFetching: false, refetch: vi.fn() }) }));
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
