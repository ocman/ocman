import { test, expect } from './fixtures';

test('deployment change shows refresh action and reloads after the 60-second countdown', async ({ mockedPage: page }) => {
  await page.clock.install();
  await page.route('**/build-hash.json', route => route.fulfill({ json: { hash: 'f'.repeat(64) } }));
  await page.goto('/sessions');
  await expect(page.getByText('Update available', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Refresh now' })).toBeVisible();
  await expect(page.getByText(/Refreshing automatically in 60 seconds/)).toBeVisible();
  await page.clock.fastForward(59_000);
  await expect(page.getByText(/Refreshing automatically in 1 second\./)).toBeVisible();
  const navigation = page.waitForEvent('load');
  await page.clock.fastForward(1000);
  await navigation;
  await expect(page).toHaveURL(/\/sessions$/);
});

test('refresh now reloads without waiting for the countdown', async ({ mockedPage: page }) => {
  await page.route('**/build-hash.json', route => route.fulfill({ json: { hash: 'f'.repeat(64) } }));
  await page.goto('/sessions');
  await expect(page.getByText('Update available', { exact: true })).toBeVisible();
  await Promise.all([page.waitForEvent('load'), page.getByRole('button', { name: 'Refresh now' }).click()]);
  await expect(page).toHaveURL(/\/sessions$/);
});
