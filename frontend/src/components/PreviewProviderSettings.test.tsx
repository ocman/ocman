// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';

function json(body: unknown, status = 200) {
  return { ok: status < 400, status, json: async () => body, text: async () => String(body) } as Response;
}

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const base = { hosts: [], configured: true, token: true, oauth: false, accounts: [] };

it('shows how each provider is set up and saves or removes a token', async () => {
  vi.resetModules();
  let linearAccounts = [{ workspaceId: 'org', workspaceName: 'Acme', accountName: 'dries', sites: [], state: 'connected' }];
  const posts: { url: string; body: unknown }[] = [];
  const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (init?.method === 'POST') {
      const body = JSON.parse(String(init.body));
      posts.push({ url, body });
      if (url === '/api/previews/disconnect') linearAccounts = [];
      if (url === '/api/previews/token' && body.token === 'bad') return Promise.resolve(json('the provider rejected this token', 400));
      return Promise.resolve(json(undefined, 204));
    }
    return Promise.resolve(json({
      providers: [
        { ...base, id: 'github', name: 'GitHub', source: 'cli', tokenHelp: 'Make a fine-grained token.' },
        { ...base, id: 'linear', name: 'Linear', accounts: linearAccounts },
        { ...base, id: 'notion', name: 'Notion', configured: false },
        { ...base, id: 'slack', name: 'Slack', token: false, oauth: true, configured: false },
      ],
      rules: [],
      hostKinds: [{ kind: 'forgejo', name: 'Forgejo', help: 'Create an access token.' }],
    }));
  });
  vi.stubGlobal('fetch', fetchMock);
  const { PreviewProviderSettings } = await import('./PreviewProviderSettings');
  render(<PreviewProviderSettings />);

  expect(await screen.findByText(/Using this machine's CLI login/)).toBeInTheDocument();
  expect(screen.getByText('dries · Acme')).toBeInTheDocument();
  expect(screen.getByText(/Not set up. Links stay plain/)).toBeInTheDocument();
  expect(screen.getByText('Not set up. Sign in to preview its links.')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Sign in to Slack' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Add token for Slack' })).toBeNull();

  fireEvent.click(screen.getByRole('button', { name: 'Add token for GitHub' }));
  expect(screen.getByText('Make a fine-grained token.')).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('GitHub token'), { target: { value: 'bad' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save token' }));
  expect(await screen.findByRole('alert')).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('GitHub token'), { target: { value: 'ghp_good' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save token' }));
  await waitFor(() => expect(screen.queryByLabelText('GitHub token')).toBeNull());
  expect(posts.at(-1)).toEqual({ url: '/api/previews/token', body: { provider: 'github', token: 'ghp_good' } });
  expect(screen.queryByDisplayValue('ghp_good')).toBeNull();

  fireEvent.click(screen.getByRole('button', { name: 'Remove Linear Acme' }));
  await waitFor(() => expect(screen.queryByText('dries · Acme')).toBeNull());
  expect(posts.at(-1)).toEqual({ url: '/api/previews/disconnect', body: { provider: 'linear', workspaceId: 'org' } });

  fireEvent.change(screen.getByRole('textbox', { name: 'Host' }), { target: { value: 'Code.Example.com' } });
  fireEvent.change(screen.getByLabelText('Forgejo Code.Example.com token'), { target: { value: 'pat' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save token' }));
  await waitFor(() => expect(posts.at(-1)).toEqual({ url: '/api/previews/token', body: { provider: 'forgejo:code.example.com', token: 'pat' } }));
});

it('reports a failed sign-in return', async () => {
  vi.resetModules();
  vi.stubGlobal('location', { ...window.location, search: '?previewAuth=error' });
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json({ providers: [], rules: [], hostKinds: [] })));
  const { PreviewProviderSettings } = await import('./PreviewProviderSettings');
  render(<PreviewProviderSettings />);
  expect(await screen.findByRole('alert')).toHaveTextContent('Signing in to the provider failed');
});
