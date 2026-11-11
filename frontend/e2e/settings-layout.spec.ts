import { test, expect } from './fixtures';

const relayUrl = 'https://relay-ocman.stenic.io';

for (const width of [1024, 1440, 390]) {
  test(`relay connection keeps its description and controls readable at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 1000 });
    await page.route('**/api/settings/webhook-relay', route => route.fulfill({ json: {
      relayUrl, defaultRelayUrl: relayUrl, hasEnrollmentToken: true,
    } }));
    await page.goto('/settings?tab=connections');
    const row = page.locator('#setting-webhook-enrollment-token');
    await expect(row.getByRole('button', { name: 'Reconnect relay' })).toBeVisible();
    await row.scrollIntoViewIfNeeded();
    const description = row.getByText(/^Sign in through the relay to authorize/);
    const status = row.getByText('Relay connected. New inboxes use the saved enrollment token.');
    const descriptionBox = (await description.boundingBox())!;
    const statusBox = (await status.boundingBox())!;
    const rowBox = (await row.boundingBox())!;
    expect(descriptionBox.width).toBeGreaterThan(Math.min(300, rowBox.width * 0.8));
    expect(statusBox.y).toBeGreaterThanOrEqual(descriptionBox.y + descriptionBox.height);
    expect(rowBox.height).toBeLessThan(350);

    await row.getByText('Manual enrollment', { exact: true }).click();
    await expect(row.getByLabel('Enrollment token')).toBeVisible();
    const field = (await row.getByLabel('Enrollment token').boundingBox())!;
    expect(field.width).toBeGreaterThan(180);
    expect(field.x + field.width).toBeLessThanOrEqual(rowBox.x + rowBox.width + 1);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    await page.getByTestId('webhook-relay-settings').screenshot({ path: test.info().outputPath(`webhook-settings-${width}.png`), animations: 'disabled' });
  });
}

test('every settings category fills the available desktop width', async ({ mockedPage: page }) => {
  const width = 1920;
  await page.setViewportSize({ width, height: 1000 });
  await page.goto('/settings');
  const navigation = page.getByRole('navigation', { name: 'Settings groups' });
  for (const name of ['App & notifications', 'Sessions', 'Agents & models', 'Permissions', 'Connections & sharing', 'Plugins', 'System']) {
    await navigation.getByRole('button', { name, exact: true }).click();
    const title = page.getByRole('heading', { level: 2, name, exact: true });
    await expect(title).toBeVisible();
    const box = (await title.boundingBox())!;
    expect(box.x + box.width, `${name} must reach the right page gutter`).toBeGreaterThan(width - 32);
  }
});

test('long relay addresses wrap without widening phone settings', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const longUrl = `https://${'relay-'.repeat(16)}example.com`;
  await page.route('**/api/settings/webhook-relay', route => route.fulfill({ json: {
    relayUrl: '', defaultRelayUrl: longUrl, hasEnrollmentToken: false,
  } }));
  await page.goto('/settings?tab=connections');
  await expect(page.getByRole('button', { name: 'Connect relay', exact: true })).toBeVisible();
  const settings = page.getByTestId('webhook-relay-settings');
  expect(await settings.evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(390);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});

test('reviewer settings form fills the expanded content area', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1920, height: 1000 });
  await page.goto('/settings?tab=permissions');
  await page.getByLabel('Reviewer API').selectOption('openai');
  const field = page.getByLabel('Endpoint URL');
  await expect(field).toBeVisible();
  const heading = (await page.getByRole('heading', { level: 2, name: 'Permissions', exact: true }).boundingBox())!;
  const input = (await field.boundingBox())!;
  expect(input.width).toBeGreaterThan(1000);
  expect(input.width).toBeGreaterThan(heading.width - 32);
});
