import { test, expect } from './fixtures';

// The sidebar leaves the Factory list narrower than the viewport, so a
// viewport breakpoint alone let the live-work row overflow sideways on
// iPad-sized screens.
test('Factory overview live work fits an iPad landscape viewport', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1000, height: 800 });
  const epic = { id: 'ship-1', status: 'open', goal: 'Ship the thing', initialProject: '/repo', progress: { requiredTotal: 1, requiredSucceeded: 0, optionalOpen: 0 }, attempts: [] };
  await page.route('/api/factory/epics', (route) => route.fulfill({ json: [epic] }));
  await page.route('/api/factory/epics/ship-1/issues', (route) => route.fulfill({ json: [] }));
  await page.route('/api/factory/queue', (route) => route.fulfill({ json: [
    { id: 'ship-1.1', epicId: 'ship-1', title: 'Qualify the sandbox release on Linux and macOS', project: '/repo', state: 'running', attemptId: 'a1', session: { platform: 'opencode', id: 'sess-abc123' } },
  ] }));

  await page.goto('/factory/overview');
  await expect(page.getByRole('link', { name: 'Open session sess-abc123' })).toBeVisible();
  const overflow = await page.getByRole('main').evaluate((main) => main.scrollWidth - main.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
});
