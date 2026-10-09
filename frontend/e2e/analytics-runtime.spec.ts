import { test, expect } from './fixtures';

for (const width of [1280, 390]) {
  test(`usage and hourly runtime charts render at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 900 });
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.route('/api/ui-usage', (route) => route.fulfill({ json: { now: Date.now() } }));
    await page.route('/api/analytics/ui-usage*', (route) => route.fulfill({ json: [
      { date: '2026-10-08', activeSeconds: 7200 }, { date: '2026-10-09', activeSeconds: 0 },
    ] }));
    await page.route('/api/analytics/agent-run-hours*', (route) => route.fulfill({ json:
      [0, 30, 90, 15, 0].map((minutes, index) => ({ timestamp: Date.UTC(2026, 9, 8, index), minutes })),
    }));
    await page.goto('/analytics/activity');
    await expect(page.getByText('2.0 hours in ocman · 1.0 hours per day')).toBeVisible();
    for (const name of ['Active hours in ocman per day', 'Agent run minutes in each chronological hour']) {
      const chart = page.getByRole('img', { name });
      await chart.scrollIntoViewIfNeeded();
      await expect(chart).toBeVisible();
      expect((await chart.boundingBox())?.width).toBeGreaterThan(200);
    }
    await expect(page.getByText(/Parallel agents add together/)).toBeVisible();
    expect(errors).toEqual([]);
    await page.screenshot({ path: test.info().outputPath('runtime-charts.png'), fullPage: true });
  });
}
