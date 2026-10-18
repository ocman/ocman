import { test, expect } from './fixtures';

test('phone navigation and the bottom browser canvas share a background', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/sessions');
  const nav = page.getByRole('navigation', { name: 'Phone navigation' });
  await expect(nav).toBeVisible();
  const background = await nav.evaluate(element => getComputedStyle(element).backgroundColor);
  await expect(page.locator('html')).toHaveCSS('background-color', background);
  await expect(page.locator('body')).toHaveCSS('background-color', background);

  const contentBackground = await page.evaluate(() => {
    const probe = document.createElement('div');
    probe.style.backgroundColor = 'var(--bg)';
    document.body.append(probe);
    const color = getComputedStyle(probe).backgroundColor;
    probe.remove();
    return color;
  });
  await expect(page.locator('.app-shell')).toHaveCSS('background-color', contentBackground);

  await nav.getByRole('link', { name: 'Projects' }).click();
  await expect(page.locator('body')).toHaveCSS('background-color', background);

  await page.setViewportSize({ width: 1024, height: 900 });
  await expect(nav).toHaveCount(0);
  await expect(page.locator('body')).toHaveCSS('background-color', contentBackground);
});
