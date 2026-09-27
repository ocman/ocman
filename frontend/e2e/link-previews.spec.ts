/**
 * e2e: authenticated link previews in the browser.
 *
 * Mocks the preview API as one server-side provider and walks the viewer
 * flow: clickable fallback link + Connect → provider consent → callback
 * redirect → rich card → reload → disconnect in Settings → fallback again.
 * Provider OAuth and API behaviour is covered by the Go lifecycle test.
 */

import { test, expect, MOCK_SESSION } from './fixtures';

const LINK = 'https://tracker.example.com/browse/ABC-42';

test('connect, preview, reload and disconnect a provider', async ({ mockedPage: page }) => {
  let connected = false;
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), (route) =>
    route.fulfill({
      json: {
        session: MOCK_SESSION,
        messages: [
          { id: 'u1', sessionId: MOCK_SESSION.id, timeCreated: 1000, data: { role: 'user' } },
          { id: 'a1', sessionId: MOCK_SESSION.id, timeCreated: 2000,
            data: { role: 'assistant', finish: 'stop', time: { created: 1500, completed: 2000 } } },
        ],
        parts: [
          { id: 'p1', sessionId: MOCK_SESSION.id, messageId: 'u1', data: { type: 'text', text: 'Where is this tracked?' } },
          { id: 'p2', sessionId: MOCK_SESSION.id, messageId: 'a1', data: { type: 'text', text: `Tracked in ${LINK}` } },
        ],
        totalMessages: 2,
      },
    }),
  );
  await page.route('/api/previews/providers*', (route) => route.fulfill({
    json: { providers: [{ id: 'tracker', name: 'Tracker', notice: 'Read-only access.', connections: connected
      ? [{ workspaceId: 'w1', workspaceName: 'Acme', accountName: 'alice', sites: [], state: 'connected' }]
      : [] }] },
  }));
  await page.route('/api/previews/resolve*', (route) => {
    const ref = { provider: 'tracker', kind: 'issue', id: 'ABC-42', url: LINK };
    if (!(route.request().postDataJSON() as { text: string }).text.includes(LINK)) return route.fulfill({ json: { previews: [] } });
    return route.fulfill({ json: { previews: [connected
      ? { ...ref, workspace: 'w1', title: 'Launch plan', status: 'In Progress', state: 'ok' }
      : { ...ref, state: 'connect' }] } });
  });
  let returnTo = '/';
  await page.route('/api/previews/connect*', (route) => {
    returnTo = (route.request().postDataJSON() as { returnTo: string }).returnTo;
    return route.fulfill({ json: { authorizeUrl: '/api/previews/oauth/callback?state=s&code=c' } });
  });
  // Stands in for the provider consent screen redirecting to the callback.
  await page.route('/api/previews/oauth/callback*', (route) => {
    connected = true;
    // WebKit cannot fulfill a 303, so redirect from the page instead.
    return route.fulfill({ contentType: 'text/html',
      body: `<script>location.replace(${JSON.stringify(`${returnTo}?previewAuth=connected`)})</script>` });
  });
  await page.route('/api/previews/disconnect*', (route) => {
    connected = false;
    return route.fulfill({ status: 204 });
  });

  await page.goto(`/session/${MOCK_SESSION.id}`);
  const notice = page.getByTestId('provider-preview-notice');
  await expect(notice.getByRole('link', { name: 'ABC-42' })).toHaveAttribute('href', LINK);
  await expect(notice).toContainText('Tracker: Connect to preview');
  await expect(page.getByTestId('provider-preview-card')).toHaveCount(0);

  await notice.getByRole('button', { name: 'Connect Tracker for ABC-42' }).click();
  await expect(page).toHaveURL(/previewAuth=connected/);
  const card = page.getByTestId('provider-preview-card');
  await expect(card).toContainText('ABC-42 Launch plan');
  await expect(card).toHaveAttribute('href', LINK);

  await page.reload();
  await expect(page.getByTestId('provider-preview-card')).toContainText('Launch plan');

  await page.goto('/settings');
  await page.getByRole('button', { name: 'Link previews' }).click();
  await expect(page.getByText('Connected: alice · Acme')).toBeVisible();
  await page.getByRole('button', { name: 'Disconnect Tracker Acme' }).click();
  await expect(page.getByText('Not connected')).toBeVisible();

  await page.goto(`/session/${MOCK_SESSION.id}`);
  await expect(page.getByTestId('provider-preview-notice').getByRole('link', { name: 'ABC-42' })).toBeVisible();
  await expect(page.getByTestId('provider-preview-card')).toHaveCount(0);
});
