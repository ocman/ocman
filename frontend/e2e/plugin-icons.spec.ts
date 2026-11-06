import { test, expect } from './fixtures';

test('optional plugin icons keep color and visibility in collapsed navigation', async ({ mockedPage: page }) => {
  await page.context().route('**/api/**', (route) => route.abort());
  await page.route('/api/capabilities', (route) => route.fulfill({ json: { hosts: [{ remoteId: 'local', pluginManagement: true, capabilities: {} }], platforms: [] } }));
  await page.route('/api/plugins/pages?*', (route) => route.fulfill({ json: [
    { ownerId: 'local', pluginId: 'org.example.documents', pluginLabel: 'Documents', icon: { bootstrap: 'journal-text' }, page: { id: 'index', label: 'Index', scope: 'plugin' } },
    { ownerId: 'local', pluginId: 'org.example.maintenance', pluginLabel: 'Maintenance', icon: { path: 'assets/wrench.svg' }, page: { id: 'jobs', label: 'Jobs', scope: 'plugin' } },
    { ownerId: 'local', pluginId: 'org.example.unknown', pluginLabel: 'Unknown', icon: { bootstrap: 'missing-icon-name' }, page: { id: 'index', label: 'Index', scope: 'plugin' } },
  ] }));
  await page.route('/api/plugins/ui', (route) => route.fulfill({ json: [] }));
  await page.route('/api/plugins/pages/call', (route) => route.fulfill({ json: { revision: '1', fields: [], values: {}, tree: {} } }));
  await page.route('/api/plugins/icon?*', (route) => route.fulfill({ contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="12" cy="12" r="8"/><path d="M4 12h16"/></svg>' }));
  await page.goto('/plugins/org.example.maintenance/jobs?ownerId=local');
  const nav = page.getByRole('navigation', { name: 'Main navigation' });
  const tools = nav.getByRole('link', { name: 'Maintenance' });
  await expect(nav.getByRole('link', { name: 'Documents' }).locator('.bi-journal-text')).toBeVisible();
  await expect(nav.getByRole('link', { name: 'Unknown' }).locator('.bi-puzzle')).toBeVisible();
  const mask = tools.locator('.plugin-nav-icon-mask');
  await expect(mask).toBeVisible();
  await expect(tools).toHaveAttribute('aria-current', 'page');
  for (const scheme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    expect(await mask.evaluate((el) => getComputedStyle(el).backgroundColor)).toBe(await tools.evaluate((el) => getComputedStyle(el).color));
  }
  const collapse = page.getByRole('button', { name: 'Collapse navigation', exact: true });
  if (await collapse.isVisible()) await collapse.click();
  await expect(page.locator('.main-nav')).toHaveClass(/collapsed/);
  await expect.poll(() => tools.locator(':scope > span').last().evaluate((el) => getComputedStyle(el).opacity)).toBe('0');
  // Opacity is not covered by Playwright's toBeVisible check.
  await expect.poll(() => mask.evaluate((el) => {
    for (let node: Element | null = el; node; node = node.parentElement) {
      if (getComputedStyle(node).opacity === '0') return false;
    }
    return true;
  })).toBe(true);
  await expect(tools.locator('svg')).toHaveCount(0);
});
