// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { api } from '../lib/api';
import { artifactsApi, previewKind, type Artifact } from '../lib/artifactsApi';
import { ArtifactDetail } from './ArtifactDetail';

vi.mock('../lib/headerContext', () => ({ usePageTitle: vi.fn() }));
vi.mock('../lib/api', () => ({ api: { sessions: vi.fn() } }));
vi.mock('../lib/artifactsApi', async (orig) => ({
  ...(await orig<typeof import('../lib/artifactsApi')>()),
  artifactsApi: { list: vi.fn(), stats: vi.fn(), get: vi.fn(), remove: vi.fn() },
}));

const file = (i: number, name: string, mime: string, size = 10) => ({ kind: 'file' as const, name, mime, size, url: `/api/artifacts/a1/files/${i}` });
const artifact: Artifact = {
  id: 'a1', title: 'Release notes', description: 'Built **today**', directory: '/repo', remoteId: 'local',
  platform: 'opencode', sessionId: 'ses-gone', createdAt: '2026-09-01T10:00:00Z',
  items: [
    file(0, 'shot.png', 'image/png'),
    file(1, 'notes.md', 'text/markdown'),
    file(2, 'main.go', 'text/x-go'),
    file(3, 'bundle.zip', 'application/zip'),
    file(4, 'report.html', 'text/html'),
    { kind: 'link', url: 'https://ci.test/run/1', label: 'CI run' },
  ],
};

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/artifacts/a1']}><Routes>
    <Route path="/artifacts/:id" element={<ArtifactDetail />} />
    <Route path="/artifacts" element={<p>list page</p>} />
  </Routes></MemoryRouter></QueryClientProvider>);
}

describe('ArtifactDetail', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.sessions).mockResolvedValue([] as never);
    vi.mocked(artifactsApi.get).mockResolvedValue(artifact);
    vi.stubGlobal('fetch', vi.fn(async (url: string) => new Response(url.endsWith('/1') ? '# Heading' : 'package main')));
  });
  afterEach(() => vi.unstubAllGlobals());

  it('renders description, links, and previews each file by mime', async () => {
    renderPage();
    expect(await screen.findByRole('heading', { name: 'Release notes' })).toBeInTheDocument();
    expect(screen.getByText('today').tagName).toBe('STRONG');
    expect(screen.getByRole('link', { name: 'CI run' })).toHaveAttribute('target', '_blank');
    expect(screen.getByTestId('artifact-preview-image')).toHaveAttribute('src', '/api/artifacts/a1/files/0');
    const frame = screen.getByTestId('artifact-preview-html');
    expect(frame).toHaveAttribute('src', '/api/artifacts/a1/files/4');
    expect(frame).toHaveAttribute('sandbox', '');
    expect(await screen.findByRole('heading', { name: 'Heading' })).toBeInTheDocument();
    expect(await screen.findByTestId('artifact-preview-text')).toHaveTextContent('package main');
    const zip = screen.getAllByTestId('artifact-file')[3];
    expect(within(zip).getByRole('link', { name: 'Open' })).toHaveAttribute('href', '/api/artifacts/a1/files/3');
    expect(within(zip).getByRole('link', { name: 'Download bundle.zip' })).toHaveAttribute('href', '/api/artifacts/a1/files/3?download=1');
    await waitFor(() => expect(screen.getByText('missing')).toBeInTheDocument());
  });

  it('deletes after confirmation and returns to the list', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true);
    vi.mocked(artifactsApi.remove).mockResolvedValue(undefined);
    renderPage();
    const del = await screen.findByRole('button', { name: 'Delete' });
    await userEvent.click(del);
    expect(artifactsApi.remove).not.toHaveBeenCalled();
    await userEvent.click(del);
    expect(artifactsApi.remove).toHaveBeenCalledWith('a1');
    expect(await screen.findByText('list page')).toBeInTheDocument();
    confirm.mockRestore();
  });

  it('navigates back to the list', async () => {
    renderPage();
    await screen.findByRole('heading', { name: 'Release notes' });
    await userEvent.click(screen.getByRole('button', { name: 'Back to artifacts' }));
    expect(await screen.findByText('list page')).toBeInTheDocument();
  });

  it('shows load errors', async () => {
    vi.mocked(artifactsApi.get).mockRejectedValue(new Error('artifact not found'));
    renderPage();
    expect(await screen.findByRole('alert')).toHaveTextContent('artifact not found');
  });

  it('classifies preview kinds', () => {
    expect(previewKind({ kind: 'file', mime: 'image/svg+xml' })).toBe('image');
    expect(previewKind({ kind: 'file', mime: 'text/html; charset=utf-8', size: 5 * 1024 * 1024 })).toBe('html');
    expect(previewKind({ kind: 'file', mime: 'application/octet-stream', name: 'x.html' })).toBe('text');
    expect(previewKind({ kind: 'file', mime: 'application/json' })).toBe('text');
    expect(previewKind({ kind: 'file', mime: 'application/octet-stream', name: 'x.ts' })).toBe('text');
    expect(previewKind({ kind: 'file', mime: 'text/plain', size: 5 * 1024 * 1024 })).toBe('none');
    expect(previewKind({ kind: 'file', mime: 'application/pdf' })).toBe('none');
  });
});
