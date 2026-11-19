import { test, expect, MOCK_SESSION } from './fixtures';

test.use({ serviceWorkers: 'block' });

test('composer has no shortcuts hint on desktop or phone', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const composer = page.getByTestId('conversation-composer');
  await expect(composer.getByRole('textbox')).toBeVisible();
  const hint = composer.getByRole('button', { name: /shortcuts/i, includeHidden: true });
  await expect(hint).toHaveCount(0);
  for (const width of [320, 390, 640]) {
    await page.setViewportSize({ width, height: 844 });
    await expect(hint).toHaveCount(0);
    await expect(composer.getByRole('textbox')).toBeVisible();
  }
});
