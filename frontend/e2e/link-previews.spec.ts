/**
 * e2e: link previews in the browser.
 *
 * Mocks the preview API as one server-side provider and walks the flow:
 * private link notice → paste a token in Settings → rich card → reload →
 * remove the token → notice again. Provider API behaviour and token
 * checking are covered by the Go tests.
 */

import { test, expect, MOCK_SESSION } from './fixtures';

const LINK = 'https://tracker.example.com/browse/ABC-42';

test('add a token, preview, reload and remove it', async ({ mockedPage: page }) => {
  let token = '';
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
    json: {
      providers: [{
        id: 'tracker', name: 'Tracker', hosts: ['tracker.example.com'], configured: true, source: 'public',
        token: true, oauth: false, tokenHelp: 'Create a read-only token.',
        accounts: token ? [{ workspaceId: 'w1', workspaceName: 'Acme', accountName: 'alice', sites: [], state: 'connected' }] : [],
      }],
      rules: [], hostKinds: [],
    },
  }));
  await page.route('/api/previews/resolve*', (route) => {
    const ref = { provider: 'tracker', kind: 'issue', id: 'ABC-42', url: LINK };
    if (!(route.request().postDataJSON() as { text: string }).text.includes(LINK)) return route.fulfill({ json: { previews: [] } });
    return route.fulfill({ json: { previews: [token
      ? { ...ref, workspace: 'w1', title: 'Launch plan', status: 'In Progress', state: 'ok' }
      : { ...ref, state: 'connect' }] } });
  });
  await page.route('/api/previews/token*', (route) => {
    token = (route.request().postDataJSON() as { token: string }).token;
    return route.fulfill({ status: 204 });
  });
  await page.route('/api/previews/disconnect*', (route) => {
    token = '';
    return route.fulfill({ status: 204 });
  });

  await page.goto(`/session/${MOCK_SESSION.id}`);
  const notice = page.getByTestId('provider-preview-notice');
  await expect(notice.getByRole('link', { name: 'ABC-42' })).toHaveAttribute('href', LINK);
  await expect(notice).toContainText('Tracker: Private. Add a token');
  await expect(page.getByTestId('provider-preview-card')).toHaveCount(0);

  await page.goto('/settings');
  await page.getByRole('button', { name: 'Link previews' }).click();
  await page.getByRole('button', { name: 'Add token for Tracker' }).click();
  await expect(page.getByText('Create a read-only token.')).toBeVisible();
  await page.getByLabel('Tracker token').fill('tok-1');
  await page.getByRole('button', { name: 'Save token' }).click();
  await expect(page.getByText('alice · Acme')).toBeVisible();
  expect(token).toBe('tok-1');

  await page.goto(`/session/${MOCK_SESSION.id}`);
  const card = page.getByTestId('provider-preview-card');
  await expect(card).toContainText('ABC-42 Launch plan');
  await expect(card).toHaveAttribute('href', LINK);
  await page.reload();
  await expect(page.getByTestId('provider-preview-card')).toContainText('Launch plan');

  await page.goto('/settings');
  await page.getByRole('button', { name: 'Link previews' }).click();
  await page.getByRole('button', { name: 'Remove Tracker Acme' }).click();
  await expect(page.getByText('alice · Acme')).toHaveCount(0);

  await page.goto(`/session/${MOCK_SESSION.id}`);
  await expect(page.getByTestId('provider-preview-notice').getByRole('link', { name: 'ABC-42' })).toBeVisible();
  await expect(page.getByTestId('provider-preview-card')).toHaveCount(0);
});

test('PR cards bundle checks and approval with one status lookup', async ({ mockedPage: page }, testInfo) => {
  const url = 'https://github.com/ocman/demo/pull/42';
  let statusReads = 0;
  let statusBatches = 0;
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), (route) => route.fulfill({ json: {
    session: MOCK_SESSION,
    messages: [{ id: 'a1', sessionId: MOCK_SESSION.id, timeCreated: 2000,
      data: { role: 'assistant', finish: 'stop', time: { created: 1500, completed: 2000 } } }],
    parts: [{ id: 'p1', sessionId: MOCK_SESSION.id, messageId: 'a1', data: { type: 'text', text: `Ready for review: ${url}` } }],
    totalMessages: 1,
  } }));
  await page.route('/api/previews/providers*', (route) => route.fulfill({ json: {
    providers: [{ id: 'github', name: 'GitHub', hosts: ['github.com'], configured: true, token: false, oauth: false, accounts: [] }],
    rules: [], hostKinds: [],
  } }));
  await page.route('/api/previews/resolve*', (route) => {
    const req = route.request().postDataJSON();
    type Lookup = { prStatus?: boolean; checksSha?: string };
    const resolve = (lookup: Lookup) => {
      if (lookup.prStatus) statusReads++;
      return { previews: [{
        provider: 'github', kind: 'pr', id: 'ocman/demo#42', url, title: 'Show PR status on preview cards',
        status: 'Open', state: 'ok', headSha: 'abc123', updatedAt: '2026-10-13T00:00:00Z', meta: ['dries'],
        ...(lookup.prStatus ? { prStatus: { mergeable: false, approved: true } } : {}),
        ...(lookup.checksSha ? { checks: { state: 'success', checks: [{ name: 'build', state: 'success' }] } } : {}),
      }] };
    };
    const requests: Lookup[] | undefined = req.requests;
    if (requests?.some((lookup) => lookup.prStatus) && requests.some((lookup) => lookup.checksSha)) statusBatches++;
    return route.fulfill({ json: requests ? { results: requests.map(resolve) } : resolve(req) });
  });
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const card = page.getByTestId('provider-preview-card');
  await expect(card).toContainText('All checks passed');
  await expect(card).toContainText('Not mergeable');
  await expect(card).toContainText('Approved');
  expect(statusReads).toBe(1);
  expect(statusBatches).toBe(1);
  await card.screenshot({ path: testInfo.outputPath('pr-preview-status.png') });
});
