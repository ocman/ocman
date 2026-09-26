import { test, expect } from './fixtures';
import type { PluginActionRequest } from '../src/lib/plugins';

test('plugin actions: discovery, confirmation, results, and unavailable invocation', async ({ mockedPage: page, browserName }) => {
  // WebKit on macOS only tabs to buttons with Alt+Tab (no Full Keyboard Access).
  const tab = browserName === 'webkit' ? 'Alt+Tab' : 'Tab';
  const requests: PluginActionRequest[] = [];
  await page.route('/api/plugins/actions?*', (route) => route.fulfill({ json: [
    { pluginId: 'org.example.report', ownerId: 'local', scope: 'hub', action: { id: 'report', label: 'Create report', placement: 'global' } },
    { pluginId: 'org.example.offline', ownerId: 'local', scope: 'hub', action: { id: 'offline', label: 'Offline action', placement: 'global' } },
  ] }));
  await page.route('/api/plugins/actions/invoke', (route) => {
    const request = route.request().postDataJSON() as PluginActionRequest;
    requests.push(request);
    if (request.actionId === 'offline') return route.fulfill({ status: 503, json: { error: { category: 'unavailable' } } });
    if (!request.confirmationToken) return route.fulfill({ status: 409, json: {
      confirmation: { text: 'Create a report for this context?', token: 'confirmation-token', expiresAt: Date.now() + 300_000 },
    } });
    return route.fulfill({ json: { results: [
      { kind: 'notice', text: 'Report ready' },
      { kind: 'link', label: 'View report', url: 'https://example.org/report' },
      { kind: 'artifact', label: 'report.txt', handle: 'artifact-1' },
      { kind: 'refresh', target: 'actions' },
      { kind: 'navigation', target: 'projects' },
    ] } });
  });
  await page.route('/api/plugins/actions/artifact?*', (route) => route.fulfill({
    headers: { 'Content-Type': 'application/octet-stream', 'Content-Disposition': 'attachment; filename="report.txt"' }, body: 'Report contents',
  }));
  await page.goto('/sessions');
  await page.getByRole('link', { name: 'Sessions', exact: true }).focus();
  await page.keyboard.press('Alt+Space');
  const search = page.getByRole('combobox', { name: 'Search commands and sessions' });
  await expect(page.getByRole('option', { name: /Create report/ })).toBeVisible();
  await search.fill('>Create report');
  await search.press('Enter');
  const dialog = page.getByRole('dialog', { name: 'Create report' });
  await expect(dialog.getByText('Create a report for this context?')).toBeVisible();
  expect(requests).toHaveLength(1);
  await page.keyboard.press(tab);
  await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused();
  await page.keyboard.press(tab);
  await expect(dialog.getByRole('button', { name: 'Confirm' })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(dialog.getByText('Report ready')).toBeVisible();
  await expect(dialog.getByRole('heading', { name: 'Create report' })).toBeFocused();
  expect(requests[1]).toEqual({ ...requests[0], confirmationToken: 'confirmation-token' });
  await expect(dialog.getByRole('link', { name: 'View report' })).toHaveAttribute('href', 'https://example.org/report');
  const downloaded = page.waitForEvent('download');
  await dialog.getByRole('link', { name: 'report.txt' }).click();
  expect((await downloaded).suggestedFilename()).toBe('report.txt');
  await expect(page).toHaveURL(/\/projects$/);
  await dialog.getByRole('button', { name: 'Close' }).click();
  await page.keyboard.press('Alt+Space');
  await search.fill('Offline action');
  await page.getByRole('option', { name: /Offline action/ }).click();
  await expect(page.getByRole('dialog', { name: 'Offline action' }).getByRole('alert')).toHaveText('This plugin is unavailable.');
  expect(requests).toHaveLength(3);
});
