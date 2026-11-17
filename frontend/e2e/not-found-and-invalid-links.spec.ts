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

test('a guessed /activity route offers the Activity panel', async ({ mockedPage: page }) => {
  await page.goto('/activity');
  await page.getByRole('region', { name: 'Page not found' }).getByRole('link', { name: 'Open Activity' }).click();
  await expect(page.getByRole('dialog', { name: 'Activity', exact: true })).toBeVisible();
});

test('a malformed path still shows the not-found page', async ({ mockedPage: page }) => {
  // The Go server falls back to index.html for /%FF, but Vite preview 404s it,
  // so reach it through client-side navigation instead.
  await page.goto('/sessions');
  await page.evaluate(() => {
    history.pushState({}, '', '/%FF');
    dispatchEvent(new PopStateEvent('popstate'));
  });
  await expect(page.getByRole('region', { name: 'Page not found' }).getByRole('link', { name: 'Go to Home' })).toBeVisible();
});

test('phones show what arrives in an empty inbox', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/inbox');
  await expect(page.getByText('Nothing needs you right now')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Set up a routine' })).toBeVisible();
});
