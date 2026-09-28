import { test, expect } from './fixtures';

const epic = (id: string, goal: string, brief: string, done: number) => ({ id, status: 'open', goal, brief, initialProject: '/repo', progress: { requiredTotal: 8, requiredSucceeded: done, optionalOpen: 0 }, attempts: [] });
const epics = [
  epic('ship-1', 'Ship the thing', 'Deliver the thing end to end, including the migration and a rollback plan for every environment we run in production today.', 3),
  epic('ship-2', 'Add Docker-isolated worktrees with mise project setup', 'Run each worktree in its own container.', 6),
  epic('ship-3', 'Per-project model list', 'Fall through to the next model when quota runs out.', 1),
  epic('ship-4', 'Slack plugin', 'Mention the bot to start a session.', 8),
];

test.beforeEach(async ({ mockedPage: page }) => {
  await page.route('/api/factory/epics', (route) => route.fulfill({ json: [...epics, { ...epic('old', 'Closed epic', '', 8), status: 'closed' }] }));
  await page.route(/\/api\/factory\/epics\/[^/]+\/issues$/, (route) => route.fulfill({ json: [] }));
  await page.route('/api/factory/queue', (route) => route.fulfill({ json: [
    { id: 'ship-1.1', epicId: 'ship-1', title: 'Qualify the sandbox release on Linux and macOS', project: '/repo', state: 'running', attemptId: 'a1', session: { platform: 'opencode', id: 'sess-abc123' } },
  ] }));
});

// The sidebar leaves the Factory list narrower than the viewport, so a
// viewport breakpoint alone let the live-work row overflow sideways on
// iPad-sized screens.
test('Factory overview fits an iPad landscape viewport', async ({ mockedPage: page }, testInfo) => {
  await page.setViewportSize({ width: 1000, height: 800 });
  await page.goto('/factory/overview');
  await expect(page.getByRole('link', { name: 'Open session sess-abc123' })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Epics in progress' }).getByRole('link')).toHaveCount(4);
  await page.screenshot({ path: testInfo.outputPath('overview.png') });
  const overflow = await page.getByRole('main').evaluate((main) => main.scrollWidth - main.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
});

test('in-progress epics render as cards, three per row', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto('/factory/overview');
  const cards = page.getByRole('region', { name: 'Epics in progress' }).getByRole('link');
  await expect(cards).toHaveCount(4);
  const tops = await cards.evaluateAll((links) => links.map((link) => Math.round(link.getBoundingClientRect().top)));
  expect(new Set(tops.slice(0, 3)).size).toBe(1);
  expect(tops[3]).toBeGreaterThan(tops[0]);
  await expect(page.getByRole('progressbar', { name: 'Slack plugin required issues done' })).toHaveAttribute('value', '8');
});
