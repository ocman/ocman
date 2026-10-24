import { test, expect, MOCK_SESSION } from './fixtures';

test('badge-counted old Factory session remains discoverable outside recent history', async ({ mockedPage: page }, testInfo) => {
  const old = { ...MOCK_SESSION, id: 'old-factory', title: 'Old Factory verification',
    status: 'interrupted', timeUpdated: Date.now() - 6 * 24 * 60 * 60 * 1000,
    factoryAttemptId: 'attempt', unreadCount: 0 };
  await page.route('/api/sessions*', route => route.fulfill({ json: [MOCK_SESSION] }));
  await page.route('/api/sessions/notify*', route => route.fulfill({ json: [old] }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const sidebar = page.getByTestId('session-sidebar');
  const attention = sidebar.getByRole('region', { name: 'Needs attention' });
  await expect(attention.getByText(old.title)).toBeVisible();
  await sidebar.getByRole('button', { name: 'Filter sessions', exact: true }).click();
  await expect(sidebar.getByRole('checkbox', { name: 'Show factory' })).not.toBeChecked();
  await sidebar.getByRole('checkbox', { name: 'Show factory' }).check();
  await expect(attention.getByText(old.title)).toBeVisible();
  await sidebar.getByRole('button', { name: 'Filter sessions', exact: true }).click();
  await page.screenshot({ path: testInfo.outputPath('sidebar-needs-attention.png') });
  await attention.getByRole('button').click();
  await expect(page).toHaveURL(/\/session\/old-factory\?platform=opencode$/);
});
