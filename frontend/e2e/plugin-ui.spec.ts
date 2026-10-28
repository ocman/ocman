import { readFileSync } from 'node:fs';
import { test, expect } from './fixtures';

const bundle = readFileSync(new URL('./plugin-ui/index.js', import.meta.url), 'utf8');
const items = [
  { id: 'lamp', name: 'Desk lamp', owner: 'Facilities', priority: 'low', status: 'active' },
  { id: 'router', name: 'Office router', owner: 'IT', priority: 'high', status: 'active' },
];

test('mounts an in-app plugin UI with host singletons, nested routes, calls and actions', async ({ mockedPage: page }) => {
  const errors: string[] = [];
  page.on('pageerror', (err) => errors.push(err.message));
  await page.route('/api/capabilities', (route) => route.fulfill({ json: {
    hosts: [{ remoteId: 'local', remoteName: 'This machine', pluginManagement: true, capabilities: {} }], platforms: [],
  } }));
  await page.route('/api/plugins/pages?*', (route) => route.fulfill({ json: [] }));
  await page.route('/api/plugins/ui', (route) => route.fulfill({ json: [{
    pluginId: 'org.example.catalog', pluginLabel: 'Catalog', ownerId: 'local', sdk: '^1.0.0', hostSdk: '1.0.0', compatible: true,
    index: { url: '/api/plugins/ui/bundle?pluginId=org.example.catalog&expose=index&sha256=e2e', sha256: 'e2e' },
    actions: [{ id: 'reset', label: 'Restore sample catalog', placement: 'global' }],
  }] }));
  await page.route('/api/plugins/ui/bundle?*', (route) => route.fulfill({ status: 200, contentType: 'text/javascript; charset=utf-8', body: bundle }));
  const calls: { pluginId: string; method: string; params: { id?: string } }[] = [];
  await page.route('/api/plugins/ui/call', (route) => {
    const body = route.request().postDataJSON();
    calls.push(body);
    return route.fulfill({ json: body.method === 'items.list' ? { items } : items.find((i) => i.id === body.params.id) });
  });
  await page.route('/api/plugins/actions/invoke', (route) => route.fulfill({ json: { results: [{ kind: 'notice', text: 'Sample catalog restored' }] } }));

  await page.goto('/plugins/org.example.catalog');
  const content = page.getByTestId('page-content');
  // The app header owns the title; plugin pages add no second heading.
  const header = page.getByTestId('header-breadcrumb');
  await expect(header).toHaveText('Catalog');
  await expect(content.getByRole('region', { name: 'Catalog' })).toBeVisible();
  await expect(content.getByRole('heading', { level: 1 })).toHaveCount(0);
  await expect(page.getByText('Mounted at /plugins/org.example.catalog')).toBeVisible();
  const table = page.getByRole('table', { name: 'Catalog items' });
  await expect(table.getByRole('link', { name: 'Office router' })).toBeVisible();
  await expect(table.getByText('high')).toBeVisible();
  // Plugins import @ocman/plugin-ui by URL; its styles must still apply.
  await expect(table.getByText('active').first()).toHaveCSS('border-radius', '999px');
  await expect(content.getByRole('region', { name: 'Items' })).toHaveCSS('display', 'grid');
  await expect(page.getByRole('link', { name: 'Catalog' })).toHaveAttribute('aria-current', 'page');

  await page.getByRole('button', { name: 'Restore (0)' }).click();
  await expect(page.getByText('Sample catalog restored')).toBeVisible();
  await page.getByRole('button', { name: 'Close' }).click();
  await expect(page.getByRole('button', { name: 'Restore (1)' })).toBeVisible();

  await table.getByRole('link', { name: 'Office router' }).click();
  await expect(page).toHaveURL(/\/plugins\/org\.example\.catalog\/items\/router$/);
  await expect(header).toHaveText('Catalog / Office router');
  await expect(page).toHaveTitle('Catalog / Office router - ocman');
  await expect(page.getByRole('rowheader', { name: 'Owner' })).toBeVisible();
  await page.getByRole('link', { name: 'Back to items' }).click();
  await expect(page).toHaveURL(/\/plugins\/org\.example\.catalog$/);
  await expect(header).toHaveText('Catalog');

  expect(calls.every((c) => c.pluginId === 'org.example.catalog')).toBe(true);
  expect(calls.map((c) => c.method)).toContain('items.get');
  expect(errors).toEqual([]);
});
