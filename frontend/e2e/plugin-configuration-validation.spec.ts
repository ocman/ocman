import { test, expect } from './fixtures';
import type { PluginRegistration } from '../src/lib/plugins';

test('missing required password opens a red, explained field without an enable request', async ({ mockedPage: page }) => {
  const plugin: PluginRegistration = {
    ownerId: 'local', approval: 'reviewed', checksum: 'fixture', enabled: false, removed: false, grants: [],
    description: { id: 'org.example.output', name: 'Output', version: '1', scope: 'owner', settings: [
      { key: 'sourcePassword', label: 'ocman password', type: 'string', required: true, secret: true },
    ] },
    configuration: { values: {}, secrets: { sourcePassword: false } },
    health: { status: 'disabled', restartCount: 0 },
  };
  let mutations = 0;
  await page.route('/api/capabilities', (route) => route.fulfill({ json: {
    platforms: [], hosts: [{ remoteId: 'local', remoteName: 'Local', pluginManagement: true, capabilities: {} }],
  } }));
  await page.route('/api/plugins?*', (route) => route.fulfill({ json: [plugin] }));
  await page.route('/api/plugins/discovery?*', (route) => route.fulfill({ json: [] }));
  await page.route('/api/plugins/org.example.output/**', (route) => {
    mutations++;
    return route.fulfill({ status: 400, body: 'private server diagnostic' });
  });
  await page.goto('/settings');
  await page.getByRole('button', { name: 'Plugins', exact: true }).click();
  await page.getByRole('button', { name: 'Enable', exact: true }).click();
  const field = page.getByLabel('ocman password', { exact: true });
  await expect(field).toBeVisible();
  await expect(field).toBeFocused();
  await expect(field).toBeInViewport();
  await expect(field).toHaveAttribute('aria-invalid', 'true');
  await expect(field).toHaveAccessibleDescription('ocman password is required and is not configured.');
  const danger = await field.evaluate((node) => {
    const probe = document.createElement('span');
    probe.style.color = 'var(--danger)';
    node.parentElement!.append(probe);
    const color = getComputedStyle(probe).color;
    probe.remove();
    return color;
  });
  await expect(field).toHaveCSS('border-top-color', danger);
  await expect(page.getByRole('button', { name: 'Save configuration', exact: true })).toBeDisabled();
  await page.getByRole('button', { name: 'Refresh health' }).click();
  await expect(field).toHaveAttribute('aria-invalid', 'true');
  await page.getByRole('button', { name: 'Configure', exact: true }).click();
  await page.getByRole('button', { name: 'Configure', exact: true }).click();
  await expect(field).toBeFocused();
  await field.fill('fixture-only-not-a-real-password');
  await expect(field).not.toHaveAttribute('aria-invalid', 'true');
  await expect(page.getByRole('button', { name: 'Save configuration', exact: true })).toBeEnabled();
  // Draft credentials must be saved, not silently included in lifecycle approval.
  await page.getByRole('button', { name: 'Enable', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Approve grants and enable' })).toHaveCount(0);
  await expect(page.getByRole('form')).toContainText('save configuration before enabling');
  expect(mutations).toBe(0);
  await expect(page.getByText('private server diagnostic')).toHaveCount(0);
});

test('public schema errors block save and a configured blank password stays valid', async ({ mockedPage: page }) => {
  const plugin: PluginRegistration = {
    ownerId: 'local', approval: 'reviewed', checksum: 'fixture', enabled: false, removed: false, grants: [],
    description: { id: 'org.example.output', name: 'Output', version: '1', scope: 'owner', settings: [
      { key: 'sourcePassword', label: 'ocman password', type: 'string', required: true, secret: true },
      { key: 'count', label: 'Count', type: 'integer' },
      { key: 'mode', label: 'Mode', type: 'string', enum: ['serial', 'http'] },
    ] },
    configuration: { values: { count: 1, mode: 'serial' }, secrets: { sourcePassword: true } },
    health: { status: 'disabled', restartCount: 0 },
  };
  let saves = 0;
  await page.route('/api/capabilities', (route) => route.fulfill({ json: {
    platforms: [], hosts: [{ remoteId: 'local', remoteName: 'Local', pluginManagement: true, capabilities: {} }],
  } }));
  await page.route('/api/plugins?*', (route) => route.fulfill({ json: [plugin] }));
  await page.route('/api/plugins/discovery?*', (route) => route.fulfill({ json: [] }));
  await page.route('/api/plugins/org.example.output/configuration?*', (route) => {
    saves++;
    expect(route.request().postDataJSON()).toEqual({ values: { count: 2, mode: 'serial' }, secrets: {} });
    return route.fulfill({ json: {} });
  });
  await page.goto('/settings');
  await page.getByRole('button', { name: 'Plugins', exact: true }).click();
  await page.getByRole('button', { name: 'Configure', exact: true }).click();
  const field = page.getByLabel('Count', { exact: true });
  await field.fill('1.5');
  await expect(field).toHaveAttribute('aria-invalid', 'true');
  await expect(field).toHaveAccessibleDescription('Enter a whole number.');
  await expect(page.getByRole('button', { name: 'Save configuration', exact: true })).toBeDisabled();
  expect(saves).toBe(0);
  const password = page.getByLabel('ocman password', { exact: true });
  await expect(password).toHaveValue('');
  await expect(password).not.toHaveAttribute('aria-invalid', 'true');
  await field.fill('2');
  await page.getByRole('button', { name: 'Save configuration', exact: true }).click();
  await expect(page.getByText('Configuration saved.', { exact: true })).toBeVisible();
  expect(saves).toBe(1);
});
