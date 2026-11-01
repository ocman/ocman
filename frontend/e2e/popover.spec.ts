import { test, expect, MOCK_SESSION } from './fixtures';

test('analytics graphs subscription history while current usage keeps its progress bars', async ({ mockedPage: page }) => {
  const now = Date.now();
  const provider = { id: 'opencode', name: 'OpenCode', plan: 'OpenCode Go', status: 'ok',
    windows: [{ name: 'Rolling', usedPercent: 40, resetsAt: new Date(now + 3600000).toISOString() }],
    budgets: [{ id: 'monthly', name: 'Monthly budget', limitUSD: 100, spentUSD: 25, remainingUSD: 75 }],
  };
  await page.route('**/api/provider-usage', route => route.fulfill({ json: { providers: [provider] } }));
  await page.route('**/api/provider-accounts', route => route.fulfill({ json: { accounts: [{
    platform: 'opencode', displayName: 'OpenCode', status: 'ok', subscriptionType: 'OpenCode Go', rateLimits: [],
  }] } }));
  await page.route('**/api/provider-usage/history?*', route => route.fulfill({ json: {
    sampleIntervalMs: 300000,
    samples: [0, 1, 2].map(index => ({ sampledAt: now - (2 - index) * 300000,
      provider: { ...provider, windows: [{ ...provider.windows[0], usedPercent: 20 + index * 10 }] },
    })),
  } }));
  await page.goto('/analytics/models');
  const content = page.getByTestId('page-content');
  await expect(content.getByRole('img', { name: 'OpenCode subscription usage history' })).toBeVisible();
  await expect(content.getByRole('progressbar')).toHaveCount(0);
  const nextChart = await content.getByRole('heading', { name: 'Model Usage', exact: true }).boundingBox();
  const historyChart = await content.getByRole('img', { name: 'OpenCode subscription usage history' }).boundingBox();
  expect(nextChart!.y).toBeGreaterThan(historyChart!.y + historyChart!.height);
  await page.setViewportSize({ width: 390, height: 844 });
  const graph = content.getByRole('img', { name: 'OpenCode subscription usage history' });
  await expect(async () => {
    const box = await graph.boundingBox();
    expect(box!.x + box!.width).toBeLessThanOrEqual(390);
  }).toPass();
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.goto('/provider-usage');
  await expect(content.getByRole('progressbar', { name: 'OpenCode Rolling usage' })).toHaveAttribute('value', '40');
  await expect(content.getByRole('img')).toHaveCount(0);
  await page.getByRole('button', { name: 'Provider usage', exact: true }).click();
  const popover = page.getByRole('dialog', { name: 'Provider usage' });
  await expect(popover.getByRole('progressbar', { name: 'OpenCode Rolling usage' })).toHaveAttribute('value', '40');
  await expect(popover.getByRole('img')).toHaveCount(0);
});

test('subscription history graphs persisted readings and keeps its period on reload', async ({ mockedPage: page }) => {
  const now = Date.now();
  await page.route('**/api/provider-usage/history?*', route => route.fulfill({ json: {
    sampleIntervalMs: 300000,
    samples: [0, 1, 2].map(index => ({ sampledAt: now - (2 - index) * 300000, provider: {
      id: 'codex', name: 'Codex', plan: 'pro', status: 'ok', windows: [{ name: '5 hours', usedPercent: 20 + index * 10, resetsAt: new Date(now + 3600000).toISOString() }],
    } })),
  } }));
  await page.goto('/provider-usage');
  await page.getByRole('tab', { name: 'History', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Subscription usage history', exact: true })).toBeVisible();
  await expect(page.getByRole('img', { name: 'Codex subscription usage history' })).toBeVisible();
  await page.getByRole('combobox', { name: 'History period' }).selectOption('30');
  await expect(page).toHaveURL(/view=history&days=30/);
  await page.reload();
  await expect(page.getByRole('combobox', { name: 'History period' })).toHaveValue('30');
  await expect(page.getByRole('img', { name: 'Codex subscription usage history' })).toBeVisible();
  await page.getByRole('tab', { name: 'Current usage', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Subscription usage history', exact: true })).not.toBeVisible();
});

test('provider usage shows the local seven-day TypeSafe estimate', async ({ mockedPage: page }) => {
  await page.route('/api/provider-usage', route => route.fulfill({ json: { providers: [{
    id: 'typesafe', name: 'TypeSafe', status: 'ok', windows: [], spend: {
      estimatedUSD: 0.0042, inputTokens: 100000, outputTokens: 5, requests: 2, unpricedRequests: 0,
      periodStart: '2026-10-12T12:00:00Z', periodEnd: '2026-10-19T12:00:00Z',
    },
  }] } }));
  await page.goto('/provider-usage');
  await expect(page.getByTestId('page-content').getByRole('heading', { name: 'Provider usage' })).toBeVisible();
  await expect(page.getByText('$0.0042')).toBeVisible();
  await expect(page.getByText('Estimated cost · last 7 days')).toBeVisible();
  await expect(page.getByText(/Not account-wide spend/)).toBeVisible();
});

test('usage and project defaults share a compact popover shell', async ({ mockedPage: page }) => {
  await page.route('**/api/sessions/prepare', (route) => route.fulfill({ json: {
    platform: 'opencode', agents: [{ name: 'build' }, { name: 'plan' }], commands: [], liveConnection: true,
    models: { hasProviders: true, models: [{ provider: 'anthropic', model: 'claude-sonnet-4-5', providerName: 'Anthropic', modelName: 'Claude Sonnet 4.5' }] },
    defaultAgent: 'build', defaultModel: 'anthropic/claude-sonnet-4-5',
  } }));
  await page.route('**/api/sessions/resolve-targets', (route) => route.fulfill({ json: {
    candidates: [{ remoteId: 'local', remoteName: 'This machine', platform: 'opencode', dir: MOCK_SESSION.directory }], remotes: [],
  } }));
  await page.route('**/api/project/settings*', (route) => route.fulfill({ json: {
    models: [], off: false, defaultAgent: 'build', defaults: { model: 'anthropic/claude-sonnet-4-5', agent: 'build', worktree: 'worktree' },
  } }));
  await page.goto(`/session/new?dir=${encodeURIComponent(MOCK_SESSION.directory)}`);
  const usageTrigger = page.getByRole('button', { name: 'Provider usage', exact: true });
  await usageTrigger.click();
  const usage = page.getByRole('dialog', { name: 'Provider usage' });
  await expect(usage).toBeFocused();
  const shellStyle = await usage.evaluate((element) => {
    const style = getComputedStyle(element);
    return { background: style.backgroundColor, padding: style.padding, radius: style.borderRadius, shadow: style.boxShadow };
  });
  await page.keyboard.press('Escape');
  await expect(usageTrigger).toBeFocused();
  const projectTrigger = page.getByRole('button', { name: 'Project quick settings' });
  await projectTrigger.click();
  await expect(page.getByRole('combobox', { name: 'Default model' })).toBeVisible();
  const project = page.getByRole('dialog', { name: 'Project quick settings' });
  await expect(project).toHaveCSS('background-color', shellStyle.background);
  await expect(project).toHaveCSS('padding', shellStyle.padding);
  await expect(project).toHaveCSS('border-radius', shellStyle.radius);
  await expect(project).toHaveCSS('box-shadow', shellStyle.shadow);
  await expect(project).toHaveCSS('border-radius', '0px');
  await expect(project).toHaveCSS('padding', '10px');
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(project).not.toBeVisible();
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await page.getByRole('button', { name: 'Open session details' }).click();
  await projectTrigger.click();
  await expect(page.getByRole('combobox', { name: 'Default model' })).toBeVisible();
  const box = await project.boundingBox();
  expect(box?.x).toBeGreaterThanOrEqual(12);
  expect(box!.x + box!.width).toBeLessThanOrEqual(378);
  await page.keyboard.press('Escape');
  await expect(projectTrigger).toBeFocused();
});
