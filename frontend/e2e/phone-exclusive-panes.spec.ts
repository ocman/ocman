import { test, expect, MOCK_SESSION } from './fixtures';

test('phone switches panes exclusively and tapping the active pane dismisses details', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/sessions');
  await page.getByRole('link', { name: /Fix the login bug/ }).first().click();
  await page.getByRole('button', { name: 'Open session details' }).click();
  const tabs = page.getByRole('tablist', { name: 'Changes views' });
  await tabs.getByRole('tab', { name: 'Bookmarks', exact: true }).click();
  await expect(tabs.getByRole('tab', { selected: true })).toHaveCount(1);
  await expect(tabs.getByRole('tab', { name: 'Bookmarks', exact: true })).toHaveAttribute('aria-selected', 'true');
  await tabs.getByRole('tab', { name: 'Session info', exact: true }).click();
  await expect(tabs.getByRole('tab', { selected: true })).toHaveCount(1);
  await tabs.getByRole('tab', { name: 'Session info', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Open session details' })).toBeVisible();
  await expect(page.getByRole('complementary', { name: 'Changes', exact: true })).toBeHidden();
  await expect(page).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}$`));
  await page.goBack();
  await expect(page).toHaveURL(/\/sessions$/);
});

test('desktop split panes become a single pane on phone without changing desktop selection', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const tabs = page.getByRole('tablist', { name: 'Changes views' });
  await tabs.getByRole('tab', { name: 'Session info', exact: true }).click();
  await expect(tabs.getByRole('tab', { selected: true })).toHaveCount(2);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: 'Open session details' }).click();
  await expect(tabs.getByRole('tab', { selected: true })).toHaveCount(1);
  await page.setViewportSize({ width: 1280, height: 900 });
  await expect(tabs.getByRole('tab', { selected: true })).toHaveCount(2);
});
