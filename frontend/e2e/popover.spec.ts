import { test, expect, MOCK_SESSION } from './fixtures';

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
