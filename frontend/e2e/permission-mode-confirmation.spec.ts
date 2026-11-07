import { test, expect, MOCK_SESSION, makeCapabilitiesWithPort, mockSessionWithLiveConnection } from './fixtures';

for (const width of [1440, 390]) {
  test(`YOLO applies without confirmation at ${width}px`, async ({ mockedPage: page }) => {
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
    await page.goto(`/session/${MOCK_SESSION.id}`);
    await page.getByRole('button', { name: 'Permission mode: Default', exact: true }).click();
    const picker = page.getByRole('dialog', { name: 'Permission mode...' });
    await picker.getByRole('option', { name: /YOLO/ }).click();
    await expect(page.getByRole('button', { name: 'Permission mode: YOLO', exact: true })).toBeVisible();
    await expect(page.getByRole('dialog', { name: 'Confirm permission mode' })).not.toBeVisible();
    await expect(picker).not.toBeVisible();
  });
}
