import { test, expect, MOCK_SESSION } from './fixtures';
test.use({ factoryBeta: true });

test('old unarchived Factory sessions stay in the flat list without an attention group', async ({ mockedPage: page }) => {
  const old = { ...MOCK_SESSION, id: 'old-factory', title: 'Old Factory verification',
    status: 'interrupted', timeUpdated: Date.now() - 6 * 24 * 60 * 60 * 1000,
    factoryAttemptId: 'attempt', unreadCount: 0 };
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

test('same-id sessions on different machines remain visible and open their owner', async ({ mockedPage: page }) => {
  const local = { ...MOCK_SESSION, id: 'shared-id', title: 'Local result' };
  const remote = { ...local, platform: 'r-box:opencode', remoteId: 'box', title: 'Remote result' };
  await page.route('/api/sessions*', route => route.fulfill({ json: [MOCK_SESSION, local, remote] }));
  await page.route('/api/sessions/notify*', route => route.fulfill({ json: [] }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const sidebar = page.getByTestId('session-sidebar');
  await expect(sidebar.getByText(local.title)).toBeVisible();
  await expect(sidebar.getByText(remote.title)).toBeVisible();
  await sidebar.getByText(remote.title).click();
  await expect(page).toHaveURL(/\/session\/shared-id\?platform=r-box%3Aopencode$/);
});
