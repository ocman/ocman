import { test, expect, MOCK_SESSION } from './fixtures';
test.use({ factoryBeta: true });

test('flat sessions use prompt or settled completion rather than streaming activity', async ({ mockedPage: page }) => {
  const rows = [
    { ...MOCK_SESSION, id: 'streaming', title: 'Streaming work', timeUpdated: 900, lastUserPromptAt: 100, status: 'busy' },
    { ...MOCK_SESSION, id: 'prompted', title: 'New prompt', timeUpdated: 300, lastUserPromptAt: 300 },
    { ...MOCK_SESSION, id: 'completed', title: 'Finished work', timeUpdated: 400, lastTurnCompletedAt: 400, status: 'done' },
    { ...MOCK_SESSION, id: 'background', title: 'Background work', timeUpdated: 800, lastUserPromptAt: 200, lastTurnCompletedAt: 500, status: 'done', backgroundTaskCount: 1 },
  ];
  await page.route('/api/sessions*', route => route.fulfill({ json: [MOCK_SESSION, ...rows] }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const sidebar = page.getByTestId('session-sidebar');
  const orderedTitles = sidebar.locator('[data-session-key]').filter({ hasText: /Streaming work|New prompt|Finished work|Background work/ });
  await expect(orderedTitles).toHaveText([
    /Finished work/, /New prompt/, /Background work/, /Streaming work/,
  ]);
  rows[3].backgroundTaskCount = 0;
  rows[3].lastTurnCompletedAt = 600;
  await page.reload();
  await expect(orderedTitles).toHaveText([
    /Background work/, /Finished work/, /New prompt/, /Streaming work/,
  ]);
});

test('old ordinary unarchived sessions stay in the flat list without an attention group', async ({ mockedPage: page }) => {
  const old = { ...MOCK_SESSION, id: 'old-session', title: 'Old verification',
    status: 'interrupted', timeUpdated: Date.now() - 6 * 24 * 60 * 60 * 1000,
    unreadCount: 0 };
  await page.route('/api/sessions*', route => route.fulfill({ json: [MOCK_SESSION, old] }));
  await page.route('/api/sessions/notify*', route => route.fulfill({ json: [old] }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const sidebar = page.getByTestId('session-sidebar');
  await expect(sidebar.getByText(old.title)).toBeVisible();
  await expect(sidebar.getByRole('region', { name: 'Needs attention' })).toHaveCount(0);
  await sidebar.getByRole('button', { name: 'Filter sessions', exact: true }).click();
  await expect(sidebar.getByRole('checkbox', { name: 'Show factory' })).toHaveCount(0);
  await sidebar.getByRole('checkbox', { name: 'Show children' }).uncheck();
  await expect(sidebar.getByText(old.title)).toBeVisible();
  await sidebar.getByRole('button', { name: 'Filter sessions', exact: true }).click();
  await page.route('/api/sessions/notify*', route => route.fulfill({ json: [] }));
  await page.reload();
  await expect(sidebar.getByText(old.title)).toBeVisible();
  await expect(sidebar.getByRole('region', { name: 'Needs attention' })).toHaveCount(0);
});

test('canonical sessions on different machines remain visible and open the selected session', async ({ mockedPage: page }) => {
  const local = { ...MOCK_SESSION, id: 'ocm_11111111-1111-4111-8111-111111111111', title: 'Local result' };
  const remote = { ...local, id: 'ocm_22222222-2222-4222-8222-222222222222', platform: 'r-box:opencode', remoteId: 'box', title: 'Remote result' };
  await page.route('/api/sessions*', route => route.fulfill({ json: [MOCK_SESSION, local, remote] }));
  await page.route('/api/sessions/notify*', route => route.fulfill({ json: [] }));
  await page.route(new RegExp(`/api/session/${remote.id}(\\?|$)`), route => route.fulfill({
    json: { session: remote, sessionTree: [remote], messages: [], parts: [], totalMessages: 0 },
  }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const sidebar = page.getByTestId('session-sidebar');
  await expect(sidebar.getByText(local.title)).toBeVisible();
  await expect(sidebar.getByText(remote.title)).toBeVisible();
  await sidebar.getByText(remote.title).click();
  await expect(page).toHaveURL(new RegExp(`/session/${remote.id}$`));
});
