import { test, expect, MOCK_SESSION } from './fixtures';

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
  const usageTrigger = page.getByRole('button', { name: 'Usage', exact: true });
  await usageTrigger.click();
  const usage = page.getByRole('dialog', { name: 'Subscription usage' });
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
  await projectTrigger.click();
  await expect(page.getByRole('combobox', { name: 'Default model' })).toBeVisible();
  const box = await project.boundingBox();
  expect(box?.x).toBeGreaterThanOrEqual(12);
  expect(box!.x + box!.width).toBeLessThanOrEqual(378);
  await page.keyboard.press('Escape');
  await expect(projectTrigger).toBeFocused();
});
