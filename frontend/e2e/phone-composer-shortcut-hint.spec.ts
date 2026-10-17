import { test, expect, MOCK_SESSION } from './fixtures';

test.use({ serviceWorkers: 'block' });

test('composer shortcut hint is desktop-only', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const hint = page.getByRole('button', { name: /for shortcuts/ });
  await expect(hint).toBeVisible();
  for (const width of [320, 390, 640]) {
    await page.setViewportSize({ width, height: 844 });
    await expect(hint).toBeHidden();
    await expect(page.getByTestId('conversation-composer').getByRole('textbox')).toBeVisible();
  }
  await page.setViewportSize({ width: 1280, height: 900 });
  await expect(hint).toBeVisible();
});
