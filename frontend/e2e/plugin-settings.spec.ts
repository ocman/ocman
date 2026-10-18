import { test, expect } from './fixtures';
import type { PluginRegistration } from '../src/lib/plugins';

test('plugin settings separates discovery and restores grants after review', async ({ mockedPage: page }, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 1800 });
  const plugin: PluginRegistration = {
    ownerId: 'local', approval: 'reviewed', checksum: 'abc123', enabled: true, removed: false, grants: [],
    description: {
      id: 'org.example.pentest', name: 'Pentest', version: '1.0.0', scope: 'owner',
      capabilities: [{ name: 'action', version: { major: 1, minor: 0 } }],
      requestedGrants: ['context.project'],
    },
    configuration: { values: {}, secrets: {} }, health: { status: 'ready', restartCount: 0 },
  };
  let approvals = 0;
  await page.route('/api/capabilities', async (route) => route.fulfill({ json: {
    platforms: [], hosts: [{ remoteId: 'local', remoteName: 'This machine', pluginManagement: true, capabilities: {} }],
  } }));
  await page.route('/api/plugins?*', (route) => route.fulfill({ json: [plugin] }));
  await page.route('/api/plugins/discovery?*', (route) => route.fulfill({ json: [] }));
  await page.route('/api/plugins/org.example.pentest/grants?*', async (route) => {
    expect(route.request().postDataJSON()).toEqual({ grants: ['context.project'] });
    expect(new URL(route.request().url()).searchParams.get('ownerId')).toBe('local');
    approvals++;
    plugin.grants = ['context.project'];
    await route.fulfill({ json: plugin });
  });
  await page.goto('/settings');
  await page.getByRole('button', { name: 'Plugins', exact: true }).click();
  const discovery = page.getByRole('region', { name: 'Discovery', exact: true });
  const pentest = page.getByRole('region', { name: 'Pentest plugin', exact: true });
  await expect(discovery.getByRole('combobox', { name: 'Plugin owner', exact: true })).toBeVisible();
  await expect(discovery.getByRole('button', { name: 'Rescan plugins' })).toBeVisible();
  await expect(pentest.getByRole('button', { name: 'Enable', exact: true })).toBeDisabled();
  await pentest.getByRole('button', { name: 'Grant permissions' }).click();
  expect(approvals).toBe(0);
  await expect(pentest.getByText(/Approve checksum/)).toContainText('abc123');
  await page.screenshot({ path: testInfo.outputPath('plugin-settings.png'), fullPage: true });
  await pentest.getByRole('button', { name: 'Approve grants', exact: true }).click();
  await expect(pentest.getByRole('button', { name: 'Grant permissions' })).toBeDisabled();
  expect(approvals).toBe(1);
  await expect(pentest.getByText('Enabled · ready')).toBeVisible();
});
