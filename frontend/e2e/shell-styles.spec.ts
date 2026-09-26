import { test, expect } from './fixtures';

test('shell keeps desktop, mobile and native navigation geometry', async ({ mockedPage: page }, testInfo) => {
  await page.goto('/sessions');
  const header = page.getByRole('banner');
  const sidebar = page.getByRole('complementary').filter({ has: page.getByRole('navigation', { name: 'Main navigation' }) });
  await expect(header).toHaveCSS('height', '44px');
  await expect(sidebar).toHaveCSS('width', '188px');
  await page.screenshot({ path: testInfo.outputPath('desktop-expanded.png') });
  await page.getByRole('button', { name: 'Collapse navigation', exact: true }).click();
  await expect(sidebar).toHaveCSS('width', '52px');
  await page.screenshot({ path: testInfo.outputPath('desktop-collapsed.png') });

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(header).toHaveCSS('padding-left', '12px');
  await page.getByRole('button', { name: 'Open navigation', exact: true }).click();
  await expect(sidebar).toHaveCSS('width', '220px');
  await expect(sidebar).toHaveCSS('transform', 'matrix(1, 0, 0, 1, 0, 0)');
  await page.screenshot({ path: testInfo.outputPath('mobile-open.png') });
  await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: 'Projects', exact: true }).click();
  await expect(sidebar).toHaveCSS('transform', 'matrix(1, 0, 0, 1, -220, 0)');

  await page.setViewportSize({ width: 1280, height: 720 });
  await page.evaluate(() => document.body.classList.add('wails-app'));
  await expect(header).toHaveCSS('padding-left', '16px');
  await expect(page.getByRole('button', { name: 'Expand navigation', exact: true })).toHaveCSS('height', '82px');
  await page.emulateMedia({ media: 'print' });
  await expect(header).toBeHidden();
  await expect(sidebar).toBeHidden();
});

for (const width of [1280, 390]) {
  test(`app header styles stay out of Factory drawer headers at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 844 });
    await page.route('/api/factory/epics', (route) => route.fulfill({ json: [] }));
    await page.route('/api/factory/formulas', (route) => route.fulfill({ json: [] }));
    await page.goto('/factory/epics');
    await page.getByRole('button', { name: 'New epic', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Create epic' });
    const heading = dialog.getByRole('heading', { name: 'Create epic' });
    await expect(heading).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath('factory-drawer.png') });
    // A drawer's semantic header must not acquire the shell's fixed height
    // or native-window drag region, including when its title wraps.
    const drawerHeader = page.getByRole('dialog', { name: 'Create epic' }).getByTestId('modal-header');
    await expect(drawerHeader).toHaveCSS('padding-bottom', '16px');
    expect((await drawerHeader.boundingBox())!.height).toBeGreaterThan((await heading.boundingBox())!.height + 16);
    await page.evaluate(() => document.body.classList.add('wails-app'));
    expect(await drawerHeader.evaluate((element) => getComputedStyle(element).getPropertyValue('--wails-draggable'))).toBe('');
  });
}
