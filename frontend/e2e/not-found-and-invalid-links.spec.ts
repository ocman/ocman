import { test, expect } from './fixtures';

test('an unknown route explains itself and links home', async ({ mockedPage: page }) => {
  await page.goto('/does-not-exist');
  const state = page.getByRole('region', { name: 'Page not found' });
  await expect(state.getByRole('heading', { name: 'Page not found' })).toBeVisible();
  await state.getByRole('link', { name: /home/i }).click();
  await expect(page).not.toHaveURL(/does-not-exist/);
});

test('a missing share link reads as one column with a plain explanation', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/import-share');
  const view = page.getByTestId('import-shared-conversation');
  const alert = view.getByRole('alert');
  await expect(alert).toContainText(/share link/i);
  await expect(alert).not.toContainText(/Invalid URL|Failed to construct/);
  const [viewBox, alertBox] = [await view.boundingBox(), await alert.boundingBox()];
  // The error spans the page column instead of sitting in a narrow side column.
  expect(alertBox!.width).toBeGreaterThan(viewBox!.width * 0.6);
});
