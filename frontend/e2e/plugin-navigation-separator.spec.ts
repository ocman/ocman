import { test, expect } from './fixtures';

test('plugin navigation has a subtle divider in expanded, collapsed and mobile navigation', async ({ mockedPage: page }) => {
  await page.route('/api/capabilities', (route) => route.fulfill({ json: { hosts: [{ remoteId: 'local', pluginManagement: true, capabilities: {} }], platforms: [] } }));
  await page.route('/api/plugins/pages?*', (route) => route.fulfill({ json: [
    { ownerId: 'local', pluginId: 'org.example.documents', pluginLabel: 'Documents', icon: { bootstrap: 'journal-text' }, page: { id: 'index', label: 'Index', scope: 'plugin' } },
    { ownerId: 'local', pluginId: 'org.example.maintenance', pluginLabel: 'Maintenance', icon: { bootstrap: 'tools' }, page: { id: 'jobs', label: 'Jobs', scope: 'plugin' } },
  ] }));
  await page.route('/api/plugins/ui', (route) => route.fulfill({ json: [] }));
  await page.goto('/');
  const expand = page.getByRole('button', { name: 'Expand navigation', exact: true });
  if (await expand.isVisible()) await expand.click();
  const nav = page.getByRole('navigation', { name: 'Main navigation', exact: true });
  const separator = nav.getByRole('separator');
  await expect(separator).toHaveCount(1);
  await expect(nav.getByRole('link', { name: 'Documents' })).toBeVisible();
  await expect.poll(() => separator.evaluate((el) => getComputedStyle(el).borderTopWidth)).toBe('1px');
  expect(await separator.evaluate((el) => el.nextElementSibling?.getAttribute('aria-label'))).toBe('Documents');
  await expect.poll(() => page.locator('.main-nav').evaluate((el) => el.getBoundingClientRect().width)).toBe(188);
  if (process.env.NAV_SCREENSHOT) await page.screenshot({ path: process.env.NAV_SCREENSHOT });
  await page.getByRole('button', { name: 'Collapse navigation', exact: true }).click();
  await expect.poll(() => page.locator('.main-nav').evaluate((el) => el.getBoundingClientRect().width)).toBe(52);
  expect(await separator.evaluate((el) => el.getBoundingClientRect().width)).toBeGreaterThan(0);
  if (process.env.NAV_COLLAPSED_SCREENSHOT) await page.screenshot({ path: process.env.NAV_COLLAPSED_SCREENSHOT });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/sessions');
  await page.getByRole('button', { name: 'More', exact: true }).click();
  await expect(page.getByRole('navigation', { name: 'More destinations', exact: true }).getByRole('separator')).toBeVisible();
  if (process.env.NAV_MOBILE_SCREENSHOT) await page.screenshot({ path: process.env.NAV_MOBILE_SCREENSHOT });
});
