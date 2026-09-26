import { test, expect } from './fixtures';

test('permission text fields show keyboard focus and invalid styling inside a shared-header dialog', async ({ mockedPage: page }) => {
  await page.route('/api/routines', (route) => route.fulfill({ json: [] }));
  await page.goto('/routines');
  await page.getByRole('button', { name: 'New routine' }).click();
  const dialog = page.getByRole('dialog', { name: 'New routine' });
  await expect(dialog.getByRole('heading', { name: 'New routine' })).toBeVisible();
  await dialog.getByText('Permissions', { exact: true }).click();
  await dialog.getByRole('button', { name: /Add rule/ }).click();
  const permission = dialog.getByLabel('Rule 1 permission');
  await permission.focus();
  await expect(permission).toHaveCSS('outline-style', 'solid');
  await expect(permission).toHaveCSS('outline-width', '2px');
  const border = await permission.evaluate((element) => getComputedStyle(element).borderTopColor);
  await permission.fill('');
  await expect(permission).toHaveAttribute('aria-invalid', 'true');
  await expect.poll(() => permission.evaluate((element) => getComputedStyle(element).borderTopColor)).not.toBe(border);
  await expect(dialog.getByRole('alert')).toHaveText('Permission is required');
});

test('shared textarea honors multiple rows and vertical resizing', async ({ mockedPage: page }) => {
  await page.goto('/factory/configuration');
  await page.getByText('Formula source', { exact: true }).click();
  const source = page.getByRole('textbox', { name: 'Formula YAML' });
  await expect(source).toBeVisible();
  await expect(source).toHaveAttribute('rows', '15');
  await expect(source).toHaveCSS('resize', 'vertical');
  expect((await source.boundingBox())!.height).toBeGreaterThan(200);
});
