import { test, expect } from './fixtures';

const request = {
  id: 'mock-request', sessionId: 'mock-session', timeCreated: Date.UTC(2026, 0, 15, 12),
  model: 'anthropic/claude', inputTokens: 150, outputTokens: 60, cacheReadTokens: 40,
  cacheWriteTokens: 0, tokensPerSecond: 30, durationMs: 2000,
  cost: 0, effectiveCost: 0.01, stopReason: 'end_turn',
};

function logData(kind: string | null) {
  return {
    kind, availableAgents: [], availableModels: [], total: kind === 'request' ? 1 : 0,
    requests: kind === 'request' ? [request] : [],
  };
}

const unavailable = { status: 503, body: 'Metric logs temporarily unavailable' };

test('restores the selected log tab on reload and Back without losing other parameters', async ({ mockedPage: page }) => {
  await page.route('/api/metric-logs*', (route) => route.fulfill({ json: logData(new URL(route.request().url()).searchParams.get('kind')) }));
  await page.goto('/analytics/logs?dir=%2Fdemo%2Frepo&owner=demo&tag=a&tag=b');
  const projectTab = page.getByRole('tab', { name: 'Project Log' });
  const requestTab = page.getByRole('tab', { name: 'Request Log' });
  await expect(projectTab).toHaveAttribute('aria-selected', 'true');
  await requestTab.click();
  await expect(page.getByText('end_turn', { exact: true })).toBeVisible();
  await page.reload();
  await expect(requestTab).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByText('end_turn', { exact: true })).toBeVisible();
  const params = new URL(page.url()).searchParams;
  expect(params.get('dir')).toBe('/demo/repo');
  expect(params.get('owner')).toBe('demo');
  expect(params.getAll('tag')).toEqual(['a', 'b']);

  await page.goBack();
  await expect(projectTab).toHaveAttribute('aria-selected', 'true');
  await page.goForward();
  await expect(requestTab).toHaveAttribute('aria-selected', 'true');
  await projectTab.click();
  await expect(projectTab).toHaveAttribute('aria-selected', 'true');
  params.delete('log');
  expect(new URL(page.url()).searchParams.toString()).toBe(params.toString());
});

test('retries a failed load with the same tab and filters without a document reload', async ({ mockedPage: page }) => {
  const failedUrls: string[] = [];
  let recovered = false;
  await page.route('/api/metric-logs*', (route) => {
    const url = new URL(route.request().url());
    const kind = url.searchParams.get('kind');
    if (kind === 'request' && !recovered) {
      failedUrls.push(url.href);
      return route.fulfill(unavailable);
    }
    return route.fulfill({ json: logData(kind) });
  });
  await page.goto('/analytics/logs?dir=%2Fdemo%2Frepo');
  await page.getByRole('combobox', { name: 'Last' }).click();
  await page.getByRole('option', { name: '7 days' }).click();
  await page.getByRole('tab', { name: 'Request Log' }).click();
  const failure = page.getByRole('alert').filter({ hasText: unavailable.body });
  await expect(failure).toBeVisible();
  await expect(page.getByRole('table')).toHaveCount(0);
  const retry = failure.getByRole('button', { name: 'Retry' });
  await expect(retry).toBeEnabled();

  const navigations: string[] = [];
  page.on('request', (outgoing) => { if (outgoing.isNavigationRequest()) navigations.push(outgoing.url()); });
  expect(new URL(failedUrls[0]).searchParams.get('days')).toBe('7');
  expect(new URL(failedUrls[0]).searchParams.get('dir')).toBe('/demo/repo');
  recovered = true;
  const retried = page.waitForResponse((response) => response.url() === failedUrls[0] && response.ok());
  await retry.press('Enter');
  await retried;
  await expect(page.getByText('end_turn', { exact: true })).toBeVisible();
  await expect(failure).toHaveCount(0);
  expect(navigations).toEqual([]);
});

test('keeps the loaded rows when refreshing the same view fails', async ({ mockedPage: page }) => {
  let failing = false;
  await page.route('/api/metric-logs*', (route) => failing
    ? route.fulfill(unavailable)
    : route.fulfill({ json: logData(new URL(route.request().url()).searchParams.get('kind')) }));
  const now = Date.UTC(2026, 0, 16);
  await page.clock.setFixedTime(now);
  await page.goto('/analytics/logs');
  await page.getByRole('tab', { name: 'Request Log' }).click();
  const row = page.getByRole('row').filter({ hasText: 'end_turn' });
  await expect(row).toBeVisible();

  failing = true;
  // Make the cached data stale before simulating the page becoming visible again.
  await page.clock.setFixedTime(now + 60_000);
  await page.evaluate(() => window.dispatchEvent(new Event('visibilitychange')));
  const warning = page.getByRole('status').filter({ hasText: unavailable.body });
  await expect(warning).toBeVisible();
  await expect(row).toBeVisible();
  failing = false;
  await warning.getByRole('button', { name: 'Retry' }).click();
  await expect(warning).toHaveCount(0);
  await expect(row).toBeVisible();
});
