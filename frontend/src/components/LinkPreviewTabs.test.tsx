// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';

afterEach(() => { vi.unstubAllGlobals(); });

it('groups link preview settings into tabs', async () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => Promise.resolve({
    ok: true, status: 200,
    json: async () => url.startsWith('/api/previews/apps')
      ? { kinds: [{ kind: 'github', name: 'GitHub', env: 'OCMAN_GITHUB_PREVIEW' }], apps: [], callbackUrl: 'http://x/cb' }
      : url.startsWith('/api/previews/providers') ? { providers: [], rules: [], hostKinds: [] } : { rules: [] },
  } as Response)));
  const { LinkPreviewTabs } = await import('./LinkPreviewTabs');
  render(<LinkPreviewTabs />);
  expect(await screen.findByText(/fetched on this machine/)).toBeInTheDocument();
  expect(screen.queryByText('Redirect URI')).toBeNull();

  const apps = screen.getByRole('tab', { name: 'Sign-in apps' });
  fireEvent.mouseDown(apps);
  fireEvent.click(apps);
  expect(await screen.findByText('Redirect URI')).toBeInTheDocument();

  const rules = screen.getByRole('tab', { name: 'Link rules' });
  fireEvent.mouseDown(rules);
  fireEvent.click(rules);
  expect(await screen.findByText('Custom link rules')).toBeInTheDocument();
});
