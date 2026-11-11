import { expect, test } from '@playwright/test';

test('feature tabs switch one panel in place and support keyboard navigation', async ({ page }, info) => {
  test.skip(info.project.name === 'no-javascript', 'Without JS, chapter links navigate to the complete tour.');
  await page.goto('/');
  const tabs = page.getByRole('tablist', { name: 'Product tour' });
  const sessions = tabs.getByRole('tab', { name: '01 / Sessions' });
  const changes = tabs.getByRole('tab', { name: '02 / Changes' });
  const insights = tabs.getByRole('tab', { name: '03 / Insights' });
  const charts = tabs.getByRole('tab', { name: '04 / Inline charts' });
  await expect(sessions).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByRole('tabpanel')).toHaveCount(1);
  await sessions.scrollIntoViewIfNeeded();
  const before = await page.evaluate(() => scrollY);
  await changes.click();
  await expect(changes).toHaveAttribute('aria-selected', 'true');
  await expect(sessions).toHaveAttribute('aria-selected', 'false');
  await expect(page.getByRole('tabpanel', { name: '02 / Changes' })).toBeVisible();
  expect(await page.evaluate(() => scrollY)).toBe(before);
  await page.keyboard.press('ArrowRight');
  await expect(insights).toBeFocused();
  await expect(insights).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('ArrowRight');
  await expect(charts).toBeFocused();
  await expect(page.getByRole('tabpanel', { name: '04 / Inline charts' })).toBeVisible();
  await page.keyboard.press('ArrowRight');
  await expect(sessions).toBeFocused();
  await page.keyboard.press('End');
  await expect(charts).toBeFocused();
  await page.keyboard.press('Home');
  await expect(sessions).toBeFocused();
  await page.keyboard.press('ArrowLeft');
  await expect(charts).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(page.getByRole('tabpanel', { name: '04 / Inline charts' })).toBeFocused();
  await expect(page.getByRole('tabpanel')).toHaveCount(1);
});

test('inline charts tab displays the real rendered screenshot', async ({ page }, info) => {
  await page.goto('/#inline-charts');
  const panel = info.project.name === 'no-javascript'
    ? page.getByRole('region', { name: 'Ask a question. See the answer.' })
    : page.getByRole('tabpanel', { name: '04 / Inline charts' });
  await expect(panel).toBeVisible();
  const screenshot = panel.getByRole('img');
  await screenshot.scrollIntoViewIfNeeded();
  await expect.poll(() => screenshot.evaluate((img: HTMLImageElement) =>
    img.complete && img.naturalWidth === 1600 && img.naturalHeight === 1400,
  )).toBe(true);
  await expect(panel.getByRole('link', { name: 'Explore inline results' })).toHaveAttribute('href', '/docs/features/mcp/#agent-generated-results');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test('a feature deep link selects its tab and survives reload', async ({ page }, info) => {
  test.skip(info.project.name === 'no-javascript', 'Static chapter anchors are covered by the fallback test.');
  await page.goto('/#changes');
  await expect(page.getByRole('tab', { name: '02 / Changes' })).toHaveAttribute('aria-selected', 'true');
  await page.getByRole('tab', { name: '03 / Insights' }).click();
  await expect(page).toHaveURL(/#analytics$/);
  await page.reload();
  await expect(page.getByRole('tabpanel', { name: '03 / Insights' })).toBeVisible();
});
