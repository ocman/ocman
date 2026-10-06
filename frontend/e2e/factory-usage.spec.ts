import { test, expect } from './fixtures';

test('Epic and Queue show phase and attempt spending', async ({ mockedPage: page }) => {
  const epic = { id: 'usage-epic', goal: 'Account for spending', status: 'open', initialProject: '/repo', models: {}, progress: { requiredTotal: 1, requiredSucceeded: 0, optionalOpen: 0 } };
  const usage = { tokens: { input: 100, output: 50, cacheRead: 200, cacheWrite: 25 }, cost: 0, estCost: 0.75 };
  await page.route('/api/factory/epics', (route) => route.fulfill({ json: [epic] }));
  await page.route('/api/factory/epics/usage-epic', (route) => route.fulfill({ json: epic }));
  await page.route(/\/api\/factory\/epics\/usage-epic\/(issues|removed-issues|proposals)$/, (route) => route.fulfill({ json: [] }));
  await page.route('/api/factory/queue', (route) => route.fulfill({ json: [{ id: 'usage-epic.1', epicId: epic.id, title: 'Validate spending', project: '/repo', state: 'running', attemptId: 'verify-attempt', session: { platform: 'opencode', id: 'verify-session' } }] }));
  await page.route('/api/factory/epics/usage-epic/usage', (route) => route.fulfill({ json: {
    total: usage, phases: { plan: usage, implement: usage, verify: usage, deliver: usage }, incomplete: false,
    attempts: [{ attemptId: 'verify-attempt', workId: 'usage-epic.1', stage: 'verify', session: { platform: 'opencode', id: 'verify-session' }, usage }],
  } }));

  await page.goto('/factory/epics/usage-epic');
  const phases = page.getByRole('table', { name: 'Phase usage for usage-epic' });
  await expect(phases.getByRole('rowheader')).toHaveText(['Total', 'Plan', 'Implement', 'Verify', 'Deliver']);
  await expect(phases.getByRole('row').filter({ has: page.getByRole('rowheader', { name: 'Total', exact: true }) }).getByRole('cell')).toHaveText(['100', '50', '200', '25', '$0.0000', '$0.7500']);
  await page.getByText('Attempts', { exact: true }).click();
  await expect(page.getByRole('table', { name: 'Attempt usage' }).getByRole('rowheader')).toContainText('Verify · usage-epic.1 · verify-attempt');
  await expect(page.getByRole('link', { name: 'Open session', exact: true })).toHaveAttribute('href', '/session/verify-session?platform=opencode');

  await page.getByRole('link', { name: 'Queue', exact: true }).click();
  await expect(page.getByText('Attempt: 375 tokens · Billed $0.0000 · Estimated $0.7500')).toBeVisible();
  await expect(phases).toBeVisible();
});
