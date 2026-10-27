import { test, expect, MOCK_SESSION, makeCapabilitiesWithPort, mockSessionWithLiveConnection } from './fixtures';

for (const width of [1440, 390]) {
  test(`YOLO confirmation stays in the permission picker's location at ${width}px`, async ({ mockedPage: page }) => {
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
    const pickerBox = (await picker.boundingBox())!;
    await picker.getByRole('option', { name: /YOLO/ }).click();
    const confirmation = page.getByRole('dialog', { name: 'Confirm permission mode' });
    await expect(confirmation).toBeVisible();
    const confirmationBox = (await confirmation.boundingBox())!;
    expect(Math.abs(confirmationBox.y - pickerBox.y)).toBeLessThan(2);
    expect(Math.abs(confirmationBox.x - pickerBox.x)).toBeLessThan(2);
    expect(confirmationBox.width).toBeCloseTo(pickerBox.width, 0);
    await expect(confirmation.getByRole('button', { name: 'Confirm', exact: true })).toBeFocused();
    await confirmation.getByRole('button', { name: 'Cancel' }).click();
    await expect(confirmation).not.toBeVisible();
  });
}
