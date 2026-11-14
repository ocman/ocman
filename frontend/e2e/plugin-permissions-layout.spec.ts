import { test, expect } from './fixtures';
import type { PluginRegistration } from '../src/lib/plugins';

const grants = ['pane.project', 'context.workspace', 'session.run', 'session.plugin', 'session.source',
  'session.send', 'session.associate', 'session.tools', 'page.plugin', 'ui.app', 'tool.agent',
  'pentest.configure', 'pentest.run'];

for (const width of [390, 768, 1440]) {
  test(`plugin grants stay below their labels at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 });
    const plugin: PluginRegistration = {
      ownerId: 'local', approval: 'reviewed', checksum: 'a'.repeat(64), enabled: false, removed: false, grants,
      description: { id: 'org.example.pentest', name: 'Pentest', version: '1.0.0', scope: 'owner', requestedGrants: grants,
        ui: { sdk: '^1.0.0', exposes: { index: { sha256: 'f'.repeat(64), size: 10 } } } },
      configuration: { values: {}, secrets: {} }, health: { status: 'disabled', restartCount: 0 },
    };
    await page.route('/api/capabilities', (route) => route.fulfill({ json: { platforms: [], hosts: [
      { remoteId: 'local', remoteName: 'This machine', pluginManagement: true, capabilities: {} },
    ] } }));
    await page.route('/api/plugins?*', (route) => route.fulfill({ json: [plugin] }));
    await page.route('/api/plugins/discovery?*', (route) => route.fulfill({ json: [] }));
    await page.goto('/settings?tab=plugins');
    await page.getByRole('link', { name: 'Pentest', exact: true }).click();
    const drawer = page.getByRole('dialog', { name: 'Pentest', exact: true });
    await drawer.getByRole('tab', { name: 'Permissions', exact: true }).click();
    for (const label of ['Requested grants', 'Current grants']) {
      const bounds = await drawer.getByText(label, { exact: true }).evaluate((element) => {
        const row = element.parentElement!.parentElement!;
        const value = document.createRange();
        value.selectNode(row.lastChild!);
        return { labelBottom: element.getBoundingClientRect().bottom, valueTop: value.getBoundingClientRect().top };
      });
      expect(bounds.valueTop).toBeGreaterThanOrEqual(bounds.labelBottom);
    }
    await drawer.getByRole('button', { name: 'Enable', exact: true }).click();
    await expect(drawer.getByText(/Approve checksum/)).toBeVisible();
    const overflow = await drawer.evaluate((element) => Array.from(element.querySelectorAll('*'))
      .filter((child) => child.clientWidth > 0 && child.scrollWidth > child.clientWidth + 1)
      .map((child) => child.tagName + '.' + child.className));
    expect(overflow).toEqual([]);
    await page.screenshot({ path: testInfo.outputPath('plugin-permissions.png'), fullPage: true });
    await drawer.getByRole('button', { name: 'Approve grants and enable' }).scrollIntoViewIfNeeded();
    await expect(drawer.getByRole('list', { name: 'Grants to approve' }).getByRole('listitem')).toHaveCount(grants.length);
    await page.screenshot({ path: testInfo.outputPath('plugin-approval.png'), fullPage: true });
  });
}
