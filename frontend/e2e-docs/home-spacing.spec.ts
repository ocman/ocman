import { expect, test } from '@playwright/test';

test('the homepage ends without excessive trailing scroll after the final call to action', async ({ page }) => {
  await page.goto('/');
  const lastLine = page.getByText('Single binary · MIT licensed', { exact: true });
  await lastLine.scrollIntoViewIfNeeded();
  await page.evaluate(() => document.fonts.ready);
  await page.evaluate(() => scrollTo(0, document.documentElement.scrollHeight));
  await expect.poll(() => lastLine.evaluate(element => {
    const bottom = element.getBoundingClientRect().bottom + scrollY;
    return Math.round(document.documentElement.scrollHeight - bottom);
  })).toBeLessThanOrEqual(144);
  const footer = page.getByRole('contentinfo');
  await expect(footer.getByRole('link', { name: 'MIT licensed' })).toBeInViewport();
  expect(await footer.evaluate(element =>
    document.documentElement.scrollHeight - (element.getBoundingClientRect().bottom + scrollY),
  )).toBeLessThanOrEqual(1);
});

test('homepage controls and panels use square edges', async ({ page }, info) => {
  await page.goto('/');
  await expect(page.getByRole('link', { name: 'Get started' })).toHaveCSS('border-radius', '0px');
  await expect(page.getByRole('link', { name: 'Take the tour' })).toHaveCSS('border-radius', '0px');
  await expect(page.getByRole('figure').first()).toHaveCSS('border-radius', '12.8px');
  if (info.project.name !== 'no-javascript') {
    await expect(page.getByRole('tablist')).toHaveCSS('border-radius', '0px');
    await expect(page.getByRole('tabpanel')).toHaveCSS('border-radius', '0px');
    for (const tab of await page.getByRole('tab').all()) {
      await expect(tab).toHaveCSS('border-radius', '0px');
    }
  }
});
