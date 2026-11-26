import { test, expect, MOCK_PROJECT } from './fixtures';

for (const width of [1280, 390]) {
  test(`project failures remain errors and retry correctly at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    let failed = true;
    await page.route('/api/projects*', route => failed
      ? route.fulfill({ status: 400, contentType: 'text/plain', body: 'Could not load projects. Restore the connection and try again.' })
      : route.fulfill({ json: [MOCK_PROJECT] }));
    await page.goto('/projects');
    const alert = page.getByRole('alert').filter({ hasText: 'Could not load projects' });
    await expect(alert).toBeVisible();
    await expect(alert.getByRole('heading', { name: 'Projects are unavailable' })).toBeVisible();
    await expect(page.getByTestId('getting-started-empty')).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Clear filters' })).toHaveCount(0);
    const retry = alert.getByRole('button', { name: 'Retry', exact: true });
    expect(await alert.evaluate(el => el.scrollWidth)).toBeLessThanOrEqual(await alert.evaluate(el => el.clientWidth));
    failed = false;
    await retry.focus();
    await retry.press('Enter');
    await expect(alert).toBeHidden();
    await expect(page.getByRole('link', { name: /projects\/myapp/ })).toBeVisible();
  });

  test(`analytics failures use shared alerts at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.route(/\/api\/(?:analytics\/(?:overview|database-sizes)|metrics\/performance|activity|hourly|models|hourly-tokens|permission-stats)(?:\?.*)?$/, route => {
      const url = new URL(route.request().url());
      return route.fulfill({ status: 400, contentType: 'text/plain', body: `Failed to load ${url.pathname}${url.search}. Data is unavailable.` });
    });
    for (const [section, count] of [['overview', 3], ['performance', 1], ['models', 3], ['activity', 3], ['permissions', 1]] as const) {
      await page.goto(`/analytics/${section}`);
      const alerts = page.getByRole('alert').filter({ hasText: 'Failed to load' });
      await expect(alerts).toHaveCount(count);
      await expect(alerts.first()).toHaveAttribute('data-inline-alert', '');
      await expect(alerts.first().getByRole('button')).toHaveCount(0);
      expect(await alerts.first().evaluate(el => el.scrollWidth)).toBeLessThanOrEqual(await alerts.first().evaluate(el => el.clientWidth));
    }
  });
}
