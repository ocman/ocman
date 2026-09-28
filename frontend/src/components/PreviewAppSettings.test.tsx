// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';

function json(body: unknown, status = 200) {
  return { ok: status < 400, status, json: async () => body, text: async () => String(body) } as Response;
}

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const kinds = [
  { kind: 'github', name: 'GitHub', env: 'OCMAN_GITHUB_PREVIEW' },
  { kind: 'forgejo', name: 'Forgejo', hosted: true, env: 'OCMAN_FORGEJO_PREVIEW_APPS' },
  { kind: 'linear', name: 'Linear', secretOptional: true, env: 'OCMAN_LINEAR_PREVIEW' },
];

it('lists apps, overrides an env app, and saves a hosted app without echoing secrets', async () => {
  vi.resetModules();
  let apps: { id: string; kind: string; host?: string; clientId: string; hasSecret: boolean; source: string; inEnv: boolean }[] = [
    { id: 'github', kind: 'github', clientId: 'env-id', hasSecret: true, source: 'env', inEnv: true },
    { id: 'linear', kind: 'linear', clientId: 'lin', hasSecret: false, source: 'settings', inEnv: false },
  ];
  const posts: { url: string; body: unknown }[] = [];
  const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (init?.method === 'POST') {
      const body = JSON.parse(String(init.body));
      posts.push({ url, body });
      if (url.endsWith('/remove')) apps = apps.filter((a) => a.id !== body.id);
      else apps = [...apps, { id: `${body.kind}:${body.host}`, kind: body.kind, host: body.host, clientId: body.clientId, hasSecret: true, source: 'settings', inEnv: false }];
    }
    return Promise.resolve(json({ kinds, apps, callbackUrl: 'http://127.0.0.1:8228/api/previews/oauth/callback' }));
  });
  vi.stubGlobal('fetch', fetchMock);
  const { PreviewAppSettings } = await import('./PreviewAppSettings');
  render(<PreviewAppSettings />);

  expect(await screen.findByText('http://127.0.0.1:8228/api/previews/oauth/callback')).toBeInTheDocument();
  expect(screen.getByText('From the environment · client ID env-id')).toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: 'Override GitHub' }));
  expect(screen.getByRole('textbox', { name: 'Client ID' })).toHaveValue('env-id');
  expect(screen.getByLabelText('Client secret')).toHaveValue('');
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

  fireEvent.change(screen.getByRole('combobox', { name: 'Provider' }), { target: { value: 'forgejo' } });
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
  fireEvent.change(screen.getByRole('textbox', { name: 'Host' }), { target: { value: 'code.example.com' } });
  fireEvent.change(screen.getByRole('textbox', { name: 'Client ID' }), { target: { value: 'fid' } });
  fireEvent.change(screen.getByLabelText('Client secret'), { target: { value: 'fsecret' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));

  expect(await screen.findByText('Forgejo (code.example.com) sign-in app')).toBeInTheDocument();
  expect(posts[0]).toEqual({ url: '/api/previews/apps/save', body: { kind: 'forgejo', host: 'code.example.com', clientId: 'fid', clientSecret: 'fsecret' } });
  expect(screen.queryByDisplayValue('fsecret')).toBeNull();

  fireEvent.click(screen.getByRole('button', { name: 'Remove Linear' }));
  await waitFor(() => expect(screen.queryByText(/client ID lin$/)).toBeNull());
  expect(posts[1]).toEqual({ url: '/api/previews/apps/remove', body: { id: 'linear' } });
});

it('shows a save error from the server', async () => {
  vi.resetModules();
  vi.stubGlobal('fetch', vi.fn().mockImplementation((_url: string, init?: RequestInit) => Promise.resolve(init?.method === 'POST'
    ? json('client secret is required', 400)
    : json({ kinds, apps: [], callbackUrl: 'http://x/cb' }))));
  const { PreviewAppSettings } = await import('./PreviewAppSettings');
  render(<PreviewAppSettings />);
  fireEvent.change(await screen.findByRole('textbox', { name: 'Client ID' }), { target: { value: 'gid' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(await screen.findByRole('alert')).toBeInTheDocument();
});
