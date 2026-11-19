import { test, expect } from './fixtures';

test('backend badges refresh in collapsed navigation and the phone More menu', async ({ mockedPage: page }) => {
  const owner = { remoteId: 'local', pluginManagement: true, capabilities: {} };
  await page.route('/api/capabilities', (route) => route.fulfill({ json: { hosts: [owner], platforms: [] } }));
  const pluginId = 'org.example.documents';
  await page.route('/api/plugins/pages?*', (route) => route.fulfill({ json: [
    { ownerId: 'local', pluginId, pluginLabel: 'Documents', navBadge: true, page: { id: 'index', label: 'Index', scope: 'plugin' } },
  ] }));
  let snapshot: object = { [pluginId]: { kind: 'dot' } };
  await page.route('/api/plugins/nav-badges?ownerId=local', (route) => route.fulfill({ json: snapshot }));

  await page.goto('/sessions');
  const nav = page.getByRole('navigation', { name: 'Main navigation' });
  const dot = nav.getByRole('link', { name: 'Documents, notification indicator' }).locator('b[aria-hidden="true"]');
  await expect(dot).toBeVisible();
  await page.getByRole('button', { name: 'Collapse navigation' }).click();
  await expect(dot).toHaveCSS('position', 'absolute');
  await expect(dot).toHaveCSS('height', '8px');

  snapshot = { [pluginId]: { kind: 'count', count: 8 } };
  await expect(nav.getByRole('link', { name: 'Documents, 8 notifications' })).toContainText('8');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: 'More', exact: true }).click();
  const more = page.getByRole('navigation', { name: 'More destinations' });
  await expect(more.getByRole('link', { name: 'Documents, 8 notifications' })).toContainText('8');
  snapshot = {};
  await expect(more.getByRole('link', { name: 'Documents', exact: true })).toBeVisible();
});
