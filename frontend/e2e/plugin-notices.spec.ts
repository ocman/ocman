import { test, expect } from './fixtures';

test('a plugin notice persists across pages, runs its declared action and clears with the snapshot', async ({ mockedPage: page }) => {
  const owner = { remoteId: 'local', pluginManagement: true, capabilities: {} };
  await page.route('/api/capabilities', (route) => route.fulfill({ json: { hosts: [owner], platforms: [] } }));
  const pluginId = 'be.nousefreak.recorder';
  const recording = { id: 'recording-1', tone: 'warning', title: 'Recording meeting', body: 'Audio is being captured.', startedAt: Date.now() - 125_000,
    actions: [{ actionId: 'stop-recording', label: 'Stop recording', placement: 'global' }] };
  let snapshot: object = { [pluginId]: { pluginLabel: 'Meeting recorder', notices: [recording] } };
  await page.route('/api/plugins/notices?ownerId=local', (route) => route.fulfill({ json: snapshot }));
  const invocations: { actionId: string; confirmationToken?: string }[] = [];
  await page.route('/api/plugins/actions/invoke', async (route) => {
    const request = route.request().postDataJSON() as { actionId: string; confirmationToken?: string };
    invocations.push(request);
    if (!request.confirmationToken) {
      await route.fulfill({ status: 409, json: { confirmation: { text: 'Stop the recording?', token: 'token-1', expiresAt: Date.now() + 60_000 } } });
      return;
    }
    snapshot = {};
    await route.fulfill({ json: { results: [{ kind: 'notice', text: 'Recording stopped.' }] } });
  });

  await page.goto('/sessions');
  const toast = page.getByRole('listitem').filter({ hasText: 'Recording meeting' });
  await expect(toast).toBeVisible();
  await expect(toast).toContainText('Meeting recorder');
  await expect(toast).toContainText('Audio is being captured.');
  await expect(toast).toContainText(/Elapsed 2m \d+s/);

  await page.goto('/projects');
  await expect(toast).toBeVisible();

  await toast.getByRole('button', { name: 'Stop recording' }).click();
  const dialog = page.getByRole('dialog', { name: 'Stop recording' });
  await expect(dialog).toContainText('Stop the recording?');
  await dialog.getByRole('button', { name: 'Confirm' }).click();
  await expect(dialog).toContainText('Recording stopped.');
  expect(invocations.map((i) => [i.actionId, i.confirmationToken])).toEqual([['stop-recording', undefined], ['stop-recording', 'token-1']]);
  await dialog.getByRole('button', { name: 'Close' }).click();
  await expect(toast).toHaveCount(0);
});

test('a dismissed notice stays hidden until its ID changes', async ({ mockedPage: page }) => {
  const owner = { remoteId: 'local', pluginManagement: true, capabilities: {} };
  await page.route('/api/capabilities', (route) => route.fulfill({ json: { hosts: [owner], platforms: [] } }));
  const notice = (id: string) => ({ 'org.example.notes': { pluginLabel: 'Notes', notices: [{ id, tone: 'info', title: 'Sync paused', actions: [] }] } });
  let snapshot: object = notice('sync-1');
  await page.route('/api/plugins/notices?ownerId=local', (route) => route.fulfill({ json: snapshot }));

  await page.goto('/sessions');
  const toast = page.getByRole('listitem').filter({ hasText: 'Sync paused' });
  await toast.getByRole('button', { name: 'Dismiss Sync paused' }).click();
  await expect(toast).toHaveCount(0);
  // Survives the next poll and navigation while the ID is unchanged.
  await page.getByRole('link', { name: 'Projects' }).first().click();
  await page.waitForResponse('/api/plugins/notices?ownerId=local');
  await expect(toast).toHaveCount(0);
  snapshot = notice('sync-2');
  await expect(toast).toBeVisible();
});
