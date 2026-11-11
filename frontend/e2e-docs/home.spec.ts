import { expect, test } from '@playwright/test';

test('homepage tour and installation remain accessible without JavaScript', async ({ page }, info) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Your coding agents.One dashboard.');
  await page.getByRole('link', { name: 'Take the tour' }).click();
  await expect(page).toHaveURL(/#product-tour$/);
  const fallback = info.project.name === 'no-javascript';
  const tour = page.getByRole(fallback ? 'navigation' : 'tablist', { name: 'Product tour' });
  for (const [chapter, title] of [
    ['01 / Sessions', 'Parallel work. Without the sprawl.'],
    ['02 / Changes', 'The conversation. And what changed.'],
    ['03 / Insights', 'Know where the effort goes.'],
    ['04 / Inline charts', 'Ask a question. See the answer.'],
  ]) {
    await tour.getByRole(fallback ? 'link' : 'tab', { name: chapter }).click();
    await expect(page.getByRole('heading', { name: title, exact: true })).toBeInViewport();
  }
  await page.getByRole('link', { name: 'Install ocman' }).click();
  await expect(page).toHaveURL(/\/docs\/introduction\/installation\/$/);
  await expect(page.getByRole('heading', { name: 'Installation', exact: true })).toBeVisible();
});

test('tour assets load, local links resolve and the page does not overflow', async ({ page, request }, info) => {
  await page.goto('/');
  const main = page.getByRole('main');
  await expect(main.getByText('Building keyboard navigation', { exact: true })).toHaveCount(0);
  await expect(main.getByText('Search endpoint reviewed', { exact: true })).toHaveCount(0);
  const images = main.getByRole('img');
  await expect(images).toHaveCount(info.project.name === 'no-javascript' ? 5 : 2);
  for (const image of await images.all()) {
    await image.scrollIntoViewIfNeeded();
    await expect.poll(() => image.evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0)).toBe(true);
    expect(await image.evaluate((img: HTMLImageElement) =>
      Number(img.getAttribute('width')) === img.naturalWidth && Number(img.getAttribute('height')) === img.naturalHeight,
    )).toBe(true);
  }
  const links = await main.getByRole('link').evaluateAll(elements => [...new Set(elements.map(a => a.getAttribute('href')!))]);
  for (const href of links.filter(href => href.startsWith('/') && !href.includes('#'))) {
    expect((await request.get(href)).ok(), href).toBe(true);
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  if (['mobile', 'short-desktop', 'reduced-motion'].includes(info.project.name)) {
    const scene = page.getByRole('tabpanel', { name: '01 / Sessions' });
    await expect(scene).toHaveCSS('position', 'static');
    expect(await page.getByRole('figure').first().evaluate(e => getComputedStyle(e).animationName)).toBe('none');
  }
});

test('desktop scrolling moves parallax layers', async ({ page }, info) => {
  test.skip(info.project.name !== 'desktop', 'Other viewport sizes have separate motion checks.');
  await page.goto('/');
  const preview = page.getByRole('figure').first();
  const before = await preview.evaluate(e => getComputedStyle(e).transform);
  await page.evaluate(() => scrollTo(0, 500));
  await expect.poll(() => preview.evaluate(e => getComputedStyle(e).transform)).not.toBe(before);
});

test('scroll effects also work on phones and shorter windows', async ({ page }, info) => {
  test.skip(!['mobile', 'short-desktop'].includes(info.project.name), 'Exercise the previously static viewport sizes.');
  await page.goto('/');
  const preview = page.getByRole('figure').first();
  const before = await preview.evaluate(e => getComputedStyle(e).transform);
  await page.evaluate(() => scrollTo(0, 300));
  await expect.poll(() => preview.evaluate(e => getComputedStyle(e).transform)).not.toBe(before);
});

test('scroll motion is always enabled without a toggle', async ({ page }, info) => {
  test.skip(info.project.name === 'no-javascript', 'JavaScript-free fallback is static.');
  await page.goto('/');
  const preview = page.getByRole('figure').first();
  await expect(page.getByRole('button', { name: /Enable animations|Pause animations/ })).toHaveCount(0);
  await expect.poll(() => preview.evaluate(e => getComputedStyle(e).transform)).not.toBe('none');
  const before = await preview.evaluate(e => getComputedStyle(e).transform);
  await page.evaluate(() => scrollTo(0, 300));
  await expect.poll(() => preview.evaluate(e => getComputedStyle(e).transform)).not.toBe(before);
});
