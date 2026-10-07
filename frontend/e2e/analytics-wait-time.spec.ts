import { test, expect } from './fixtures';

for (const width of [1280, 390]) {
  test(`waiting-time chart renders at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 900 });
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.route('/api/metrics/performance*', (route) => route.fulfill({ json: {
      availableAgents: ['build', 'plan'], availableModels: [],
      summary: { completedRequests: 2, successfulRequests: 2, errorRequests: 0, errorRate: 0, p50DurationMs: 5000, p95DurationMs: 10000, costPerSuccessfulRequest: 0 },
      series: [], stopReasons: [],
      agents: [
        { agent: 'build', totalDurationMs: 10000, agentDurationMs: 2000, toolDurationMs: 7000, unknownDurationMs: 1000 },
        { agent: 'plan', totalDurationMs: 5000, agentDurationMs: 5000, toolDurationMs: 0, unknownDurationMs: 0 },
      ],
    } }));
    await page.goto('/analytics/performance');
    await expect(page.getByText('Waiting Time by Agent (s)', { exact: true })).toBeVisible();
    const chart = page.getByRole('img', { name: 'Total waiting time by agent, split into agent response, tools, and unknown timing' });
    await expect(chart).toBeVisible();
    expect((await chart.boundingBox())?.width).toBeGreaterThan(200);
    await expect(page.getByText(/Parallel tools count once/)).toBeVisible();
    expect(errors).toEqual([]);
  });
}
