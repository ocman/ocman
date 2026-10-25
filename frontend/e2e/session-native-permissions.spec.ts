import { test, expect, MOCK_SESSION, makeCapabilitiesWithPort, mockSessionWithLiveConnection } from './fixtures';

test('native Auto changes independently of ocman restrictions', async ({ mockedPage: page }) => {
  const caps = makeCapabilitiesWithPort();
  await page.route('**/api/capabilities', route => route.fulfill({ json: {
    ...caps,
    platforms: caps.platforms.map(platform => ({ ...platform, capabilities: {
      ...platform.capabilities, permissionRules: true, nativeAutoPermission: true,
    } })),
  } }));
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
    session: mockSessionWithLiveConnection(), messages: [], parts: [], totalMessages: 0,
  } }));
  const rules = [{ permission: 'edit', pattern: '*', action: 'deny' }];
  let nativeMode = 'default';
  await page.route(`**/api/session/${MOCK_SESSION.id}/permission-rules`, route => {
    expect(route.request().method()).toBe('GET');
    return route.fulfill({ json: { rules, nativeMode: { mode: nativeMode } } });
  });
  await page.route(`**/api/session/${MOCK_SESSION.id}/native-permission-mode`, async route => {
    const body = route.request().postDataJSON();
    expect(body.rules).toBeUndefined();
    nativeMode = body.mode;
    await route.fulfill({ status: 204 });
  });
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await page.getByRole('button', { name: 'Permission mode: Custom', exact: true }).click();
  await page.getByText('Enable native Auto', { exact: true }).click();
  await expect(page.getByRole('button', { name: 'Permission mode: Custom + native Auto', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Permission mode: Custom + native Auto', exact: true }).click();
  await page.getByText('Disable native Auto', { exact: true }).click();
  await expect(page.getByRole('button', { name: 'Permission mode: Custom', exact: true })).toBeVisible();
});
