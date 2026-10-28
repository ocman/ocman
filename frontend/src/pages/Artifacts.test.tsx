// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import { api } from '../lib/api';
import { artifactsApi, formatBytes, type Artifact } from '../lib/artifactsApi';
import { Artifacts } from './Artifacts';

const created: Array<() => void> = [];
vi.mock('../lib/headerContext', () => ({ usePageTitle: vi.fn() }));
vi.mock('../lib/useGlobalEvents', () => ({ onArtifactCreated: (cb: () => void) => { created.push(cb); return () => undefined; } }));
vi.mock('../lib/api', () => ({ api: { projects: vi.fn(), sessions: vi.fn() } }));
vi.mock('../lib/artifactsApi', async (orig) => ({
  ...(await orig<typeof import('../lib/artifactsApi')>()),
  artifactsApi: { list: vi.fn(), stats: vi.fn(), get: vi.fn(), remove: vi.fn() },
}));

const artifact = (id: string, over: Partial<Artifact> = {}): Artifact => ({
  id, title: `Report ${id}`, directory: '/repo', remoteId: 'local', createdAt: '2026-09-01T10:00:00Z',
  platform: 'opencode', sessionId: 'ses-live',
  items: [{ kind: 'file', name: 'a.txt', mime: 'text/plain', size: 2048, url: `/api/artifacts/${id}/files/0` }, { kind: 'link', url: 'https://x.test' }],
  ...over,
});

function LocationProbe() {
  const location = useLocation();
  const navigate = useNavigate();
  return <><output data-testid="location">{location.pathname}{location.search}</output><button onClick={() => navigate(-1)}>Browser back</button></>;
}

function renderPage(entry = '/artifacts') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}><MemoryRouter initialEntries={[entry]}><LocationProbe /><Routes>
    <Route path="/artifacts" element={<Artifacts />} />
    <Route path="/artifacts/:id" element={<p>Artifact details</p>} />
  </Routes></MemoryRouter></QueryClientProvider>);
}

describe('Artifacts page', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    created.length = 0;
    vi.mocked(api.projects).mockResolvedValue([{ directory: '/repo', archived: false }, { directory: '/other', archived: false }] as never);
    vi.mocked(api.sessions).mockResolvedValue([{ id: 'ses-live' }] as never);
    vi.mocked(artifactsApi.stats).mockResolvedValue({ count: 3, totalBytes: 5 * 1024 * 1024 });
  });

  it('lists artifacts with session state, sizes and total storage, and pages with Load more', async () => {
    vi.mocked(artifactsApi.list)
      .mockResolvedValueOnce({ artifacts: [artifact('a1'), artifact('a2', { sessionId: 'ses-gone' })], nextCursor: 'c1' })
      .mockResolvedValueOnce({ artifacts: [artifact('a3', { sessionId: undefined, platform: undefined })], nextCursor: '' });
    renderPage();
    expect(await screen.findByRole('link', { name: 'Report a1' })).toHaveAttribute('href', '/artifacts/a1');
    expect(screen.getByTestId('artifact-stats')).toHaveTextContent('3 artifacts · 5.0 MiB stored');
    expect(screen.getAllByText('2.0 KiB')).toHaveLength(2);
    await waitFor(() => expect(screen.getByText('missing')).toBeInTheDocument());
    expect(screen.getByRole('link', { name: 'Session' })).toHaveAttribute('href', '/session/ses-live?platform=opencode');

    await userEvent.click(screen.getByRole('button', { name: 'Load more' }));
    expect(await screen.findByText('Report a3')).toBeInTheDocument();
    expect(artifactsApi.list).toHaveBeenLastCalledWith({ directory: '', q: '', cursor: 'c1' }, expect.anything());
    expect(screen.getAllByTestId('artifact-row')).toHaveLength(3);
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument();
  });

  it('sends the project filter and debounced search to the server', async () => {
    vi.mocked(artifactsApi.list).mockResolvedValue({ artifacts: [artifact('a1')], nextCursor: '' });
    renderPage();
    await screen.findByText('Report a1');
    await screen.findByRole('option', { name: '/other' });
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Project' }), '/other');
    await waitFor(() => expect(artifactsApi.list).toHaveBeenLastCalledWith({ directory: '/other', q: '' }, expect.anything()));
    await userEvent.type(screen.getByRole('searchbox', { name: 'Search artifacts' }), 'deploy');
    await waitFor(() => expect(artifactsApi.list).toHaveBeenLastCalledWith({ directory: '/other', q: 'deploy' }, expect.anything()));
    expect(vi.mocked(artifactsApi.list).mock.calls.some(([p]) => p?.q === 'd')).toBe(false);
  });

  it('refreshes on ocman.artifact.created and shows the empty state', async () => {
    vi.mocked(artifactsApi.list).mockResolvedValueOnce({ artifacts: [], nextCursor: '' }).mockResolvedValue({ artifacts: [artifact('new')], nextCursor: '' });
    renderPage();
    expect(await screen.findByRole('heading', { name: 'No artifacts saved yet' })).toBeInTheDocument();
    act(() => created.forEach((cb) => cb()));
    expect(await screen.findByText('Report new')).toBeInTheDocument();
  });

  it('retries a failed read without showing a false empty state', async () => {
    vi.mocked(artifactsApi.list).mockRejectedValueOnce(new Error('List unavailable')).mockResolvedValue({ artifacts: [artifact('recovered')], nextCursor: '' });
    renderPage();
    expect(await screen.findByRole('alert')).toHaveTextContent('List unavailable');
    expect(screen.getByRole('heading', { name: 'Artifacts are unavailable' })).toBeVisible();
    expect(screen.queryByText('No artifacts yet.')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('link', { name: 'Report recovered' })).toBeVisible();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(artifactsApi.list).toHaveBeenLastCalledWith({ directory: '', q: '' }, expect.anything());
  });

  it('distinguishes filtered empty results and clears the filters', async () => {
    vi.mocked(artifactsApi.list).mockResolvedValue({ artifacts: [], nextCursor: '' });
    renderPage();
    await userEvent.type(screen.getByRole('searchbox', { name: 'Search artifacts' }), 'missing');
    expect(await screen.findByRole('heading', { name: 'No matching artifacts' })).toBeVisible();
    expect(screen.queryByText('No artifacts yet.')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(screen.getByRole('searchbox', { name: 'Search artifacts' })).toHaveValue('');
    expect(await screen.findByRole('heading', { name: 'No artifacts saved yet' })).toBeVisible();
  });

  it('restores filters from a link, preserves unrelated parameters and returns to the filtered list', async () => {
    vi.mocked(artifactsApi.list).mockResolvedValue({ artifacts: [artifact('a1')], nextCursor: '' });
    renderPage('/artifacts?q=coverage&directory=%2Frepo&source=review');
    expect(screen.getByRole('searchbox', { name: 'Search artifacts' })).toHaveValue('coverage');
    expect(screen.getByRole('combobox', { name: 'Project' })).toHaveValue('/repo');
    await userEvent.click(await screen.findByRole('link', { name: 'Report a1' }));
    expect(screen.getByTestId('location')).toHaveTextContent('/artifacts/a1?q=coverage&directory=%2Frepo&source=review');
    await userEvent.click(screen.getByRole('button', { name: 'Browser back' }));
    expect(await screen.findByRole('searchbox', { name: 'Search artifacts' })).toHaveValue('coverage');
    await userEvent.clear(screen.getByRole('searchbox', { name: 'Search artifacts' }));
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Project' }), '');
    await waitFor(() => expect(artifactsApi.list).toHaveBeenLastCalledWith({ directory: '', q: '' }, expect.anything()));
    expect(screen.getByTestId('location')).toHaveTextContent('/artifacts?source=review');
  });

  it('clears only list filters and retains an unknown project from a shared URL', async () => {
    vi.mocked(artifactsApi.list).mockResolvedValue({ artifacts: [], nextCursor: '' });
    renderPage('/artifacts?q=missing&directory=%2Farchived&source=review');
    expect(screen.getByRole('combobox', { name: 'Project' })).toHaveValue('/archived');
    await userEvent.click(await screen.findByRole('button', { name: 'Clear filters' }));
    expect(await screen.findByRole('heading', { name: 'No artifacts saved yet' })).toBeVisible();
    expect(screen.getByTestId('location')).toHaveTextContent('/artifacts?source=review');
  });

  it.each([true, false])('retains successful data during a failed refresh, empty=%s', async (empty) => {
    vi.mocked(artifactsApi.list).mockResolvedValueOnce({ artifacts: empty ? [] : [artifact('a1')], nextCursor: '' });
    renderPage();
    if (empty) await screen.findByRole('heading', { name: 'No artifacts saved yet' });
    else await screen.findByRole('link', { name: 'Report a1' });
    vi.mocked(artifactsApi.list).mockRejectedValue(new Error('Refresh failed'));
    act(() => created.forEach((callback) => callback()));
    expect(await screen.findByRole('alert')).toHaveTextContent('Refresh failed');
    expect(screen.queryByRole('heading', { name: 'Artifacts are unavailable' })).not.toBeInTheDocument();
    if (empty) expect(screen.getByRole('heading', { name: 'No artifacts saved yet' })).toBeVisible();
    else expect(screen.getByRole('link', { name: 'Report a1' })).toBeVisible();
  });

  it('retries the failed next page without losing loaded rows', async () => {
    vi.mocked(artifactsApi.list)
      .mockResolvedValueOnce({ artifacts: [artifact('a1')], nextCursor: 'next' })
      .mockRejectedValueOnce(new Error('Next page failed'))
      .mockResolvedValueOnce({ artifacts: [artifact('a2')], nextCursor: '' });
    renderPage();
    await userEvent.click(await screen.findByRole('button', { name: 'Load more' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Next page failed');
    expect(screen.getByRole('link', { name: 'Report a1' })).toBeVisible();
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('link', { name: 'Report a2' })).toBeVisible();
    expect(screen.getByRole('link', { name: 'Report a1' })).toBeVisible();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(artifactsApi.list).toHaveBeenLastCalledWith({ directory: '', q: '', cursor: 'next' }, expect.anything());
  });

  it('uses skeleton rows before the first response', () => {
    vi.mocked(artifactsApi.list).mockReturnValue(new Promise(() => {}));
    renderPage();
    expect(screen.getByRole('table', { name: 'Loading artifacts' })).toHaveAttribute('aria-busy', 'true');
  });

  it('formats byte sizes', () => {
    expect(formatBytes(12)).toBe('12 B');
    expect(formatBytes(1536)).toBe('1.5 KiB');
    expect(formatBytes(200 * 1024 * 1024)).toBe('200 MiB');
  });
});
