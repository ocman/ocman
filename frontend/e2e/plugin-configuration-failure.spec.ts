import { test, expect } from './fixtures';
import type { PluginRegistration } from '../src/lib/plugins';

test('activation failure remains visible beside Save even when refreshing health fails', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  const plugin: PluginRegistration = {
    ownerId: 'local', approval: 'reviewed', checksum: 'fixture', enabled: true, removed: false, grants: [],
    description: {
      id: 'org.example.output', name: 'Output', version: '1', scope: 'owner',
      settings: [
        { key: 'serialEnabled', label: 'Serial enabled', type: 'boolean' },
        { key: 'password', label: 'Source password', type: 'string', secret: true },
      ],
    },
    configuration: { values: { serialEnabled: false }, secrets: { password: false } },
    health: { status: 'ready', restartCount: 0 },
  };
  let refreshFails = false;
  await page.route('/api/capabilities', (route) => route.fulfill({ json: {
    platforms: [], hosts: [{ remoteId: 'local', remoteName: 'Local', pluginManagement: true, capabilities: {} }],
  } }));
  await page.route('/api/plugins?*', (route) => refreshFails
    ? route.fulfill({ status: 503, body: 'private refresh error' })
    : route.fulfill({ json: [plugin] }));
  await page.route('/api/plugins/discovery?*', (route) => route.fulfill({ json: [] }));
  await page.route('/api/plugins/org.example.output/configuration?*', (route) => {
    expect(route.request().postDataJSON()).toEqual({ values: { serialEnabled: true }, secrets: {} });
    return route.fulfill({ status: 503, body: 'private activation error' });
  });
  await page.goto('/settings');
  await page.getByRole('button', { name: 'Plugins', exact: true }).click();
  await page.getByRole('button', { name: 'Configure', exact: true }).click();
  await page.getByLabel('Serial enabled', { exact: true }).selectOption('true');
  await page.getByRole('button', { name: 'Save configuration', exact: true }).click();
  const form = page.getByRole('form', { name: 'Plugin configuration' });
  const error = form.getByRole('alert');
  await expect(error).toContainText('Could not confirm configuration was saved and activated');
  await expect(error).toBeFocused();
  await expect(error).toBeInViewport();
  await expect(form.getByLabel('Serial enabled', { exact: true })).toHaveValue('false');
  await expect(form.getByLabel('Source password', { exact: true })).toHaveValue('');
  await expect(page.getByText('Enabled · ready')).toBeVisible();
  refreshFails = true;
  await page.getByRole('button', { name: 'Refresh health' }).click();
  await expect(page.getByText(/Could not load plugins for this owner/)).toBeVisible();
  await expect(error).toBeVisible();
  refreshFails = false;
  await page.getByRole('button', { name: 'Refresh health' }).click();
  await expect(page.getByText(/Could not load plugins for this owner/)).toHaveCount(0);
  await expect(error).toBeVisible();
  await expect(page.getByText(/private (activation|refresh) error/)).toHaveCount(0);
});
