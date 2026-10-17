import { test, expect } from './fixtures';

test.use({ serviceWorkers: 'block' });

for (const width of [320, 390, 640]) {
  for (const [path, title] of [['/sessions', 'Sessions'], ['/projects', 'Projects'], ['/settings', 'Settings']]) {
    test(`${title} has a compact page header at ${width}px`, async ({ mockedPage: page }) => {
      await page.setViewportSize({ width, height: 844 });
      await page.goto(path);
      const header = page.getByRole('banner');
      await expect(header.getByRole('heading', { level: 1 })).toHaveText(title);
      await expect(header).toHaveCSS('padding-top', '8px');
      expect((await header.boundingBox())!.height).toBe(60);
      await expect(page.getByRole('navigation', { name: 'Phone navigation' })).toBeVisible();
    });
  }
}

test('desktop page headers retain their existing height', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto('/sessions');
  expect((await page.getByRole('banner').boundingBox())!.height).toBe(44);
});
