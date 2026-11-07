import { test, expect, MOCK_SESSION, makeCapabilitiesWithPort, mockSessionWithLiveConnection, mockStream, promptAsked } from './fixtures';
import type { Locator } from '@playwright/test';

async function expectOpaqueTree(dialog: Locator) {
  await expect(dialog).toBeVisible();
  await expect.poll(() => dialog.evaluate(element => {
    const ancestors = [];
    for (let node: Element | null = element; node; node = node.parentElement) {
      const opacity = getComputedStyle(node).opacity;
      if (Number(opacity) < 1) ancestors.push({ className: node.className, opacity });
    }
    return ancestors;
  })).toEqual([]);
}

for (const width of [1440, 390]) {
  test(`permission picker stays opaque with a pending prompt at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 1100 });
    const caps = makeCapabilitiesWithPort();
    await page.route('**/api/capabilities', route => route.fulfill({ json: {
      ...caps,
      platforms: caps.platforms.map(platform => ({ ...platform, capabilities: {
        ...platform.capabilities, permissionRules: true,
      } })),
    } }));
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
      session: mockSessionWithLiveConnection(), messages: [], parts: [], totalMessages: 0,
    } }));
    await page.route(`**/api/session/${MOCK_SESSION.id}/permission-rules`, route => route.fulfill({ json: { rules: [] } }));
    await mockStream(page, MOCK_SESSION.id, [promptAsked(MOCK_SESSION.id, {
      id: 'permission-opacity', kind: 'permission', permission: { permission: 'bash', patterns: ['git status'] },
    })]);
    await page.goto(`/session/${MOCK_SESSION.id}`);
    await expectOpaqueTree(page.getByRole('dialog', { name: 'Permission required' }));
    await page.getByRole('button', { name: 'Permission mode: Default', exact: true }).click();
    const picker = page.getByRole('dialog', { name: 'Permission mode...' });
    await expectOpaqueTree(picker);
    await picker.getByRole('option', { name: /YOLO/ }).click();
    await expect(page.getByRole('button', { name: 'Permission mode: YOLO', exact: true })).toBeVisible();
    await expect(page.getByRole('dialog', { name: 'Confirm permission mode' })).not.toBeVisible();
    await expectOpaqueTree(page.getByRole('dialog', { name: 'Permission required' }));
  });
}
