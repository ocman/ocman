import { test, expect } from './fixtures';

test('Factory beta opt-in controls routes, navigation and analytics across reloads', async ({ mockedPage: page }, testInfo) => {
  await page.goto('/factory/epics');
  await expect(page.getByRole('heading', { name: 'Feature unavailable', level: 2 })).toBeVisible();
  const nav = page.getByRole('navigation', { name: 'Main navigation' });
  await expect(nav.getByRole('link', { name: 'Factory', exact: true })).toHaveCount(0);
  await expect(page.getByRole('heading', { level: 1 })).not.toContainText('Factory');
  await page.goto('/inbox?category=factory');
  await expect(page.getByRole('radio', { name: 'Factory', exact: true })).toHaveCount(0);
  await page.goto('/settings?tab=app');
  await expect(page.getByText('Factory Inbox unread', { exact: true })).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath('factory-disabled.png') });

  await page.goto('/analytics/overview');
  for (const label of ['Factory epics', 'Factory issues', 'Factory attempts']) {
    await expect(page.getByText(label, { exact: true })).toHaveCount(0);
  }
  await expect(page.getByText('Routine runs', { exact: true })).toBeVisible();

  await page.goto('/factory/overview');
  await page.getByRole('link', { name: 'Open beta settings' }).click();
  await expect(page.getByRole('heading', { name: 'Beta features' })).toBeVisible();
  await page.getByRole('checkbox', { name: 'Enable Factory beta' }).click();
  await expect(page.getByRole('checkbox', { name: 'Enable Factory beta' })).toBeChecked();
  await expect(nav.getByRole('link', { name: 'Factory', exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('factory-enabled.png') });
  await page.reload();
  await expect(page.getByRole('checkbox', { name: 'Enable Factory beta' })).toBeChecked();
  await page.goto('/inbox');
  await expect(page.getByRole('radio', { name: 'Factory', exact: true })).toBeVisible();

  await page.goto('/analytics/overview');
  for (const label of ['Factory epics', 'Factory issues', 'Factory attempts']) {
    await expect(page.getByText(label, { exact: true })).toBeVisible();
  }
  await nav.getByRole('link', { name: 'Factory', exact: true }).click();
  await expect(page.getByRole('navigation', { name: 'Factory', exact: true })).toBeVisible();
  await page.goto('/settings?tab=app');
  await page.getByRole('checkbox', { name: 'Enable Factory beta' }).click();
  await expect(page.getByRole('checkbox', { name: 'Enable Factory beta' })).not.toBeChecked();
  await page.reload();
  await expect(nav.getByRole('link', { name: 'Factory', exact: true })).toHaveCount(0);
  await page.goto('/analytics/overview');
  await expect(page.getByText('Factory attempts', { exact: true })).toHaveCount(0);
  await page.goto('/factory/queue');
  await expect(page.getByRole('heading', { name: 'Feature unavailable', level: 2 })).toBeVisible();
});
