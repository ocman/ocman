import { test, expect } from './fixtures';
import type { PluginRegistration } from '../src/lib/plugins';

test('plugin list opens a drawer and restores grants after review', async ({ mockedPage: page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
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
  await expect(page.getByRole('combobox', { name: 'Plugin owner', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Rescan plugins' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Grant permissions' })).toHaveCount(0);
  await page.getByRole('link', { name: 'Pentest', exact: true }).click();
  await page.getByRole('tab', { name: 'Permissions', exact: true }).click();
  const pentest = page.getByRole('region', { name: 'Pentest plugin', exact: true });
  await expect(pentest.getByRole('button', { name: 'Enable', exact: true })).toBeDisabled();
  await pentest.getByRole('button', { name: 'Grant permissions' }).click();
  expect(approvals).toBe(0);
  await expect(pentest.getByText(/Approve checksum/)).toContainText('abc123');
  await page.screenshot({ path: testInfo.outputPath('plugin-settings.png'), fullPage: true });
  await pentest.getByRole('button', { name: 'Approve grants', exact: true }).click();
  await expect(pentest.getByRole('button', { name: 'Grant permissions' })).toBeDisabled();
  expect(approvals).toBe(1);
  await expect(page.getByRole('dialog', { name: 'Pentest', exact: true })).toContainText('Enabled · Healthy');
});

for (const phone of [false, true]) {
  test(`many plugins remain searchable with a ${phone ? 'phone' : 'desktop'} drawer`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize(phone ? { width: 390, height: 844 } : { width: 1440, height: 1000 });
    const names = ['Forgejo', 'GitHub', 'Slack', 'Sentry', 'Linear', 'Notion', 'Docker', 'Obsidian', 'Calendar', 'Jira', 'Bitbucket', 'Webhooks'];
    const icons = ['git', 'github', 'slack', 'bug', 'kanban', 'journal-text', 'box', 'journal', 'calendar', 'list-check', 'git', 'link-45deg'];
    const catalog: PluginRegistration[] = Array.from({ length: 32 }, (_, i) => ({
      ownerId: 'local', approval: 'reviewed', checksum: 'abc123', enabled: i % 4 !== 0, removed: false, grants: [],
      description: { id: `org.example.plugin-${i}`, name: names[i] || `Plugin ${String(i).padStart(2, '0')}`, icon: { bootstrap: icons[i] || 'puzzle' },
        version: '1.0.0', scope: 'owner', requestedGrants: i === 0 ? ['context.project'] : [],
        settings: [{ key: 'url', label: 'Server URL', type: 'string' },
          ...(i === 0 ? [{ key: 'token', label: 'API token', type: 'string' as const, secret: true }, { key: 'repository', label: 'Default repository', type: 'string' as const }] : [])] },
      configuration: { values: { url: 'https://forgejo.example.com', ...(i === 0 ? { repository: 'dries/ocman' } : {}) }, secrets: i === 0 ? { token: true } : {} },
      health: { status: i === 3 ? 'unhealthy' : i % 4 === 0 ? 'disabled' : 'ready', restartCount: i === 3 ? 3 : 0,
        ...(i === 3 ? { lastError: 'Authentication failed. Check the API token in Settings.' } : {}) },
    }));
    await page.route('/api/capabilities', (route) => route.fulfill({ json: { platforms: [], hosts: [
      { remoteId: 'local', remoteName: 'MacBook Pro (local)', pluginManagement: true, capabilities: {} },
    ] } }));
    await page.route('/api/plugins?*', (route) => route.fulfill({ json: catalog }));
    await page.route('/api/plugins/discovery?*', (route) => route.fulfill({ json: [] }));
    await page.route('/api/plugins/*/stderr?*', (route) => route.fulfill({ json: { stderr: [
      '10:42:11 Connecting to sentry.example.com', '10:42:12 Request failed: HTTP 401 Unauthorized',
      '10:42:12 Authentication rejected by server', '10:42:12 Plugin entered error state',
      '10:43:12 Retrying connection', '10:43:13 Request failed: HTTP 401 Unauthorized',
    ].join('\n') } }));
    await page.route('/api/plugins/org.example.plugin-0/enable?*', (route) => {
      expect(route.request().postDataJSON()).toEqual({ approval: 'reviewed', grants: ['context.project'] });
      catalog[0].enabled = true; catalog[0].health.status = 'ready'; catalog[0].grants = ['context.project'];
      return route.fulfill({ json: {} });
    });
    await page.route('/api/plugins/org.example.plugin-0/disable?*', (route) => {
      catalog[0].enabled = false; catalog[0].health.status = 'disabled';
      return route.fulfill({ json: {} });
    });
    await page.goto('/settings?tab=plugins');
    await expect(page.getByText('Page 1 of 2', { exact: false })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath('plugin-list.png'), fullPage: true });
    await page.getByRole('searchbox', { name: 'Search plugins', exact: true }).fill('forgejo');
    await page.getByRole('link', { name: 'Forgejo', exact: true }).click();
    const drawer = page.getByRole('dialog', { name: 'Forgejo', exact: true });
    await expect(drawer.getByRole('textbox', { name: 'Server URL' })).toBeVisible();
    await expect(drawer).toContainText('Host: MacBook Pro (local)');
    await expect(drawer).toBeInViewport();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const toggle = drawer.getByRole('checkbox', { name: 'Enable Forgejo' });
    await toggle.click();
    await expect(drawer.getByText(/Approve checksum/)).toBeVisible();
    await expect(toggle).not.toBeChecked();
    await drawer.getByRole('button', { name: 'Approve grants and enable' }).click();
    await expect(toggle).toBeChecked();
    await drawer.getByRole('tab', { name: 'Settings', exact: true }).click();
    await expect(drawer.getByRole('tab', { name: 'Settings', exact: true })).toHaveAttribute('aria-selected', 'true');
    await expect(drawer.getByRole('textbox', { name: 'Server URL' })).toBeVisible();
    await expect(toggle).toBeEnabled();
    await expect(toggle).toBeChecked();
    await page.screenshot({ path: testInfo.outputPath('plugin-drawer.png'), fullPage: true });
    await toggle.click();
    await expect(toggle).not.toBeChecked();
    await drawer.getByRole('textbox', { name: 'Server URL' }).fill('https://changed.example.com');
    await drawer.getByRole('button', { name: 'Close plugin details' }).click();
    await page.getByRole('button', { name: 'Keep editing' }).click();
    await expect(drawer.getByRole('textbox', { name: 'Server URL' })).toHaveValue('https://changed.example.com');
    await drawer.getByRole('button', { name: 'Cancel', exact: true }).click();
    await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
    await expect(drawer).toHaveCount(0);
    await expect(page.getByRole('searchbox', { name: 'Search plugins', exact: true })).toHaveValue('forgejo');
    await page.getByRole('searchbox', { name: 'Search plugins', exact: true }).fill('sentry');
    await page.getByRole('link', { name: 'Sentry', exact: true }).click();
    await page.getByRole('button', { name: 'View logs', exact: true }).click();
    await expect(page.getByLabel('Recent stderr', { exact: true })).toContainText('HTTP 401 Unauthorized');
    await expect(page.getByRole('button', { name: 'Copy logs' })).toBeEnabled();
    await expect(page.getByText('Plugin reported an error', { exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath('plugin-logs.png'), fullPage: true });
    await page.getByRole('button', { name: 'Open settings', exact: true }).click();
    await expect(page.getByRole('tab', { name: 'Settings', exact: true })).toHaveAttribute('aria-selected', 'true');
  });
}
