import { test, expect, MOCK_SESSION, MOCK_PROJECT } from './fixtures';

test('shows the remote host pill in the flat sidebar, session title and composer', async ({ mockedPage: page }, testInfo) => {
  const remote = { remoteId: 'build-host', remoteName: 'Build host' };
  const session = { ...MOCK_SESSION, ...remote };
  await page.route('/api/sessions*', route => route.fulfill({ json: [session] }));
  await page.route('/api/projects*', route => route.fulfill({ json: [{ ...MOCK_PROJECT, ...remote }] }));
  await page.route(new RegExp(`/api/session/${session.id}(\\?.*)?$`), route => route.fulfill({ json: {
    session, messages: [], parts: [], totalMessages: 0, defaultAgent: '', defaultModel: '',
  } }));
  await page.goto(`/session/${session.id}`);
  const sidebar = page.getByTestId('session-sidebar');
  await expect(sidebar.getByLabel('Build host', { exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { level: 1 }).getByLabel('Build host', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Build host', { exact: true })).toHaveCount(3);
  await page.screenshot({ path: testInfo.outputPath('session-host-pills.png') });
});
