import { test, expect } from './fixtures';
import type { PageDocument } from '../src/lib/pluginPages';

test('plugin contributions reuse host controls for validation, lists and linked details', async ({ mockedPage: page }, testInfo) => {
  await page.context().route('**/api/**', (route) => route.abort());
  await page.route('/api/capabilities', (route) => route.fulfill({ json: {
    hosts: [{ remoteId: 'local', remoteName: 'This machine', pluginManagement: true, capabilities: {} }], platforms: [],
  } }));
  await page.route('/api/plugins/pages?*', (route) => route.fulfill({ json: [
    { ownerId: 'local', pluginId: 'org.example.services', page: { id: 'services', label: 'Services', validate: true } },
  ] }));
  await page.route('/api/plugins/actions?*', (route) => route.fulfill({ json: [] }));
  let doc: PageDocument = {
    revision: 'r1', configured: true, values: { name: 'API', replicas: 2, environment: 'staging' }, tree: { available: false },
    fields: [
      { key: 'name', label: 'Service name', type: 'text', required: true },
      { key: 'replicas', label: 'Replicas', type: 'integer', required: true, minimum: 1, maximum: 20 },
      { key: 'environment', label: 'Environment', type: 'select', options: [{ value: 'staging', label: 'Staging' }, { value: 'production', label: 'Production' }] },
    ],
    collection: { title: 'Registered services', columns: [
      { key: 'name', label: 'Name', display: 'text' }, { key: 'state', label: 'State', display: 'status' },
    ], rows: [{ id: 'service-a', values: { name: 'API', state: 'open' } }], nextCursor: 'next-page' },
  };
  let saved = 0;
  let paginated = false;
  await page.route('/api/plugins/pages/call', (route) => {
    const input = route.request().postDataJSON();
    expect(input.ownerId).toBe('local');
    expect(input.request.projectId).toBe('project-a');
    if (input.request.itemId) {
      expect(input.request.itemId).toBe('service-a');
      return route.fulfill({ json: { revision: doc.revision, values: {}, tree: {}, detail: {
        title: 'API service details', properties: [{ label: 'Replicas', display: 'number', value: 3 }, { label: 'Enabled', display: 'boolean', value: true }],
      } } });
    }
    if (input.request.cursor) { expect(input.request.cursor).toBe('next-page'); paginated = true; }
    if (input.method === 'save') {
      if (input.request.values.name === 'taken') return route.fulfill({ json: { ...doc, issues: [{ path: '/name', message: 'This name is already used.' }] } });
      expect(input.request.values.replicas).toBe(3);
      expect(input.request.values.environment).toBe('production');
      doc = { ...doc, revision: 'r2', values: input.request.values };
      saved++;
    }
    return route.fulfill({ json: doc });
  });
  await page.goto('/plugins/org.example.services/services?ownerId=local&projectId=project-a');
  await page.getByRole('button', { name: 'Next page' }).click();
  await expect(page.getByRole('button', { name: 'First page' })).toBeVisible();
  await page.getByLabel('Service name', { exact: true }).fill('taken');
  await page.getByRole('spinbutton', { name: 'Replicas' }).fill('3');
  await page.getByLabel('Environment', { exact: true }).selectOption('production');
  await expect(page.getByRole('link', { name: 'View service-a' })).toHaveCount(0);
  await page.getByRole('button', { name: 'Save configuration' }).click();
  await expect(page.getByRole('alert')).toHaveText('This name is already used.');
  await expect(page.getByLabel('Service name', { exact: true })).toHaveValue('taken');
  expect(saved).toBe(0);
  await page.getByLabel('Service name', { exact: true }).fill('API');
  await page.getByRole('button', { name: 'Save configuration' }).click();
  await expect(page.getByText('Saved.', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'First page' })).toHaveCount(0);
  expect(saved).toBe(1);
  await page.setViewportSize({ width: 1440, height: 1100 });
  const workspaceScreenshot = testInfo.outputPath('plugin-workspace.png');
  await page.screenshot({ path: workspaceScreenshot, fullPage: true });
  await testInfo.attach('plugin-workspace', { path: workspaceScreenshot, contentType: 'image/png' });
  await page.getByRole('button', { name: 'Next page' }).click();
  await expect(page.getByRole('button', { name: 'First page' })).toBeVisible();
  expect(paginated).toBe(true);
  await page.getByRole('link', { name: 'View service-a' }).click();
  await expect(page).toHaveURL(/ownerId=local&projectId=project-a&itemId=service-a$/);
  await expect(page.getByRole('heading', { name: 'API service details' })).toBeVisible();
  await expect(page.getByRole('cell', { name: 'Yes', exact: true })).toBeVisible();
  const detailScreenshot = testInfo.outputPath('plugin-detail.png');
  await page.screenshot({ path: detailScreenshot, fullPage: true });
  await testInfo.attach('plugin-detail', { path: detailScreenshot, contentType: 'image/png' });
  await page.reload();
  await expect(page.getByRole('heading', { name: 'API service details' })).toBeVisible();
  await page.getByRole('button', { name: 'Back to list' }).click();
  await expect(page.getByRole('link', { name: 'View service-a' })).toBeVisible();
});
