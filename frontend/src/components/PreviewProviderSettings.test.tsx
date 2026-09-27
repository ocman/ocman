// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';

function json(body: unknown, status = 200) {
  return { ok: status < 400, status, json: async () => body } as Response;
}

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

it('shows connection states and disconnects with an immediate reload', async () => {
  vi.resetModules();
  let connections = [
    { workspaceId: 'w1', workspaceName: 'Acme', accountName: 'alice', sites: [{ id: 's1', name: 'Docs' }], state: 'connected' },
    { workspaceId: 'w2', workspaceName: 'Beta', accountName: 'alice', sites: [], state: 'expired' },
  ];
  const fetchMock = vi.fn().mockImplementation((url: string) => {
    if (url.startsWith('/api/previews/providers')) {
      return Promise.resolve(json({ providers: [{ id: 'mock', name: 'Tracker', connections }, { id: 'wiki', name: 'Wiki', notice: 'Forgejo OAuth has no granular scopes.', connections: [] }] }));
    }
    connections = connections.slice(1);
    return Promise.resolve(json(undefined, 204));
  });
  vi.stubGlobal('fetch', fetchMock);
  const { PreviewProviderSettings } = await import('./PreviewProviderSettings');
  render(<PreviewProviderSettings />);

  expect(await screen.findByText('Connected: alice · Acme, Docs')).toBeInTheDocument();
  expect(screen.getByText('Expired: alice · Beta')).toBeInTheDocument();
  expect(screen.getByText('Not connected')).toBeInTheDocument();
  expect(screen.getByText('Forgejo OAuth has no granular scopes.')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Reconnect Tracker' })).toHaveClass('oc-button');
  expect(screen.getByRole('button', { name: 'Connect Wiki' })).toBeEnabled();

  fireEvent.click(screen.getByRole('button', { name: 'Disconnect Tracker Acme' }));
  await waitFor(() => expect(screen.queryByText(/Acme/)).toBeNull());
  expect(fetchMock).toHaveBeenCalledWith('/api/previews/disconnect?remoteId=local', expect.objectContaining({
    method: 'POST', body: JSON.stringify({ provider: 'mock', workspaceId: 'w1' }),
  }));
});

it('reports a failed consent return and an empty provider list', async () => {
  vi.resetModules();
  vi.stubGlobal('location', { ...window.location, search: '?previewAuth=error' });
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json({ providers: [] })));
  const { PreviewProviderSettings } = await import('./PreviewProviderSettings');
  render(<PreviewProviderSettings />);
  expect(await screen.findByText('No preview integrations are configured on this server.')).toBeInTheDocument();
  expect(screen.getByRole('alert')).toHaveTextContent('Connecting the provider failed');
});
