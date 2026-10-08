import { test, expect } from './fixtures';

for (const width of [1280, 390]) {
  test(`parallel-session chart renders at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 900 });
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.route('/api/analytics/session-concurrency*', (route) => route.fulfill({ json: {
      bucketMs: 3_600_000,
      series: [0, 1, 3, 2, 0].map((sessions, index) => ({ timestamp: Date.UTC(2026, 9, 8, index), sessions })),
    } }));
    await page.goto('/analytics/activity');
    await expect(page.getByText('Active Parallel Sessions', { exact: true })).toBeVisible();
    const chart = page.getByRole('img', { name: 'Peak active parallel sessions over time' });
    await expect(chart).toBeVisible();
    expect((await chart.boundingBox())?.width).toBeGreaterThan(200);
    await expect(page.getByText(/excludes idle gaps and unfinished messages/)).toBeVisible();
    expect(errors).toEqual([]);
  });
}
