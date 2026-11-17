import { test, expect } from './fixtures';

const longName = `${'a'.repeat(100)}-workspace`;
const project = {
  directory: `/home/demo/src/${longName}`, sessions: 1, requests: 3, inputTokens: 10, outputTokens: 20,
  cacheReadTokens: 30, cacheWriteTokens: 4, totalTokens: 64, totalDurationMs: 1000, avgTokensPerSec: 20,
  cost: 0, calcCost: 0.01, effectiveCost: 0.01, models: ['anthropic/claude'], errorCount: 0, lastRequestTime: 1,
};

test('long project names truncate inside the sticky log column', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.route('/api/metric-logs*', (route) => route.fulfill({ json: {
    kind: 'project', total: 1, availableAgents: [], availableModels: [], projects: [project],
  } }));
  await page.goto('/analytics/logs');
  const link = page.getByRole('link', { name: new RegExp(longName) });
  await expect(link).toBeVisible();
  const label = link.locator(':scope > *').first();
  const linkBox = (await link.boundingBox())!;
  const labelBox = (await label.boundingBox())!;
  expect(labelBox.x + labelBox.width).toBeLessThanOrEqual(linkBox.x + linkBox.width + 1);
  const sessions = page.getByRole('row').nth(1).getByRole('cell').nth(1);
  expect(labelBox.x + labelBox.width).toBeLessThanOrEqual((await sessions.boundingBox())!.x);
});
