import { test, expect } from './fixtures';

for (const width of [1280, 390]) {
  test(`usage disclosure and opt-out persist at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 900 });
    let enabled = true;
    const updates: unknown[] = [];
    await page.route('**/api/settings/usage-collection', async (route) => {
      if (route.request().method() === 'POST') {
        const body = route.request().postDataJSON();
        updates.push(body);
        enabled = body.enabled;
      }
      await route.fulfill({ json: { enabled, relayUrl: 'https://relay.example.com/v1/usage', version: '0.275.0' } });
    });
    await page.goto('/settings?tab=privacy');
    const toggle = page.getByRole('checkbox', { name: 'Send usage reports' });
    await expect(toggle).toBeChecked();
    await expect(page.getByText(/Reports are pseudonymous, not anonymous/)).toBeVisible();
    await expect(page.getByText('https://relay.example.com/v1/usage')).toBeVisible();
    await toggle.click();
    await expect(toggle).not.toBeChecked();
    expect(updates).toEqual([{ enabled: false }]);
    await page.reload();
    await expect(toggle).not.toBeChecked();
    await expect(toggle).toBeEnabled();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    await page.goto('/settings?tab=relay');
    await expect(page.getByRole('heading', { name: 'Public sharing', exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Webhooks', exact: true })).toBeVisible();
    await page.goto('/settings?tab=connections');
    await expect(page.getByRole('heading', { name: 'Public sharing', exact: true })).toBeHidden();
  });
}
