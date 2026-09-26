import { test, expect, MOCK_SESSION } from './fixtures';

test('session menu hands focus to Share and restores its trigger on close', async ({ mockedPage: page }) => {
  await page.route('/api/session/*/share-links*', (route) => route.fulfill({ json: [] }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const trigger = page.getByRole('button', { name: 'Session actions' });
  await trigger.click();
  await page.getByRole('menuitem', { name: 'Share link…' }).click();
  const dialog = page.getByRole('dialog', { name: 'Public share link' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Create share link' })).toBeFocused();
  await expect(page.getByRole('menu')).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(trigger).toBeFocused();
});
