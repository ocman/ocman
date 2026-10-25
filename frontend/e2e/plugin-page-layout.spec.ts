import { test, expect } from './fixtures';
import type { PageDocument } from '../src/lib/pluginPages';

for (const width of [390, 1280]) {
  test(`generic collection and detail tables stay inside the page at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 });
    await page.route('/api/capabilities', (route) => route.fulfill({ json: { hosts: [{ remoteId: 'local', pluginManagement: true, capabilities: {} }], platforms: [] } }));
    await page.route('/api/plugins/pages?*', (route) => route.fulfill({ json: [{ ownerId: 'local', pluginId: 'org.example.inventory', page: { id: 'inventory', label: 'Example inventory', scope: 'plugin' } }] }));
    const longValue = 'artifact-segment-without-a-break/'.repeat(20);
    const columns = ['Name', 'Environment', 'Replicas', 'Endpoint', 'Revision', 'Owner'];
    const doc: PageDocument = { revision: 'v1', fields: [], values: {}, tree: {},
      detail: { title: 'Service metadata', properties: [{ label: 'Storage key', display: 'text', value: longValue }, { label: 'Description', display: 'text', value: 'Shared service metadata for an inventory page.' }] },
      collection: { title: 'Services', columns: columns.map((label, i) => ({ key: `column${i}`, label, display: 'text' })), rows: [{ id: 'service-a', values: Object.fromEntries(columns.map((_, i) => [`column${i}`, `Value-${i}-${longValue}`])) }] },
    };
    await page.route('/api/plugins/pages/call', (route) => route.fulfill({ json: doc }));
    await page.goto('/plugins/org.example.inventory/inventory?ownerId=local');
    await expect(page.getByRole('heading', { name: 'Service metadata', exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Services', exact: true })).toBeVisible();
    const content = page.getByTestId('page-content');
    const geometry = await content.evaluate((el) => ({ width: el.clientWidth, scrollWidth: el.scrollWidth }));
    expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.width);
    // Wide tables must still be readable through their own scroll frames.
    for (const name of ['Service metadata', 'Services']) {
      const table = page.getByRole('region', { name, exact: true }).getByRole('table');
      const sizes = await table.evaluate((el) => ({ width: el.parentElement!.clientWidth, scrollWidth: el.parentElement!.scrollWidth }));
      if (name === 'Services' && width === 390) expect(sizes.scrollWidth).toBeGreaterThan(sizes.width);
      if (sizes.scrollWidth > sizes.width) {
        await table.evaluate((el) => { el.parentElement!.scrollLeft = el.parentElement!.scrollWidth; });
        expect(await table.evaluate((el) => el.parentElement!.scrollLeft)).toBeGreaterThan(0);
        await table.evaluate((el) => { el.parentElement!.scrollLeft = 0; });
      }
    }
    await page.screenshot({ path: testInfo.outputPath(`generic-tables-${width}.png`), fullPage: true });
  });
}
