import { test, expect, MOCK_PROJECT, MOCK_SESSION } from './fixtures';

test('project sessions show a retryable unavailable state after a 503', async ({ mockedPage: page }) => {
  let unavailable = true;
  await page.route(/\/api\/sessions(?:\?|$)/, route => unavailable
    ? route.fulfill({ status: 503, json: { error: 'Session inventory unavailable' } })
    : route.fulfill({ json: [MOCK_SESSION] }));
  await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}`);
  const panel = page.getByRole('tabpanel', { name: 'Sessions' });
  await expect(panel.getByRole('heading', { name: 'Sessions are unavailable' })).toBeVisible();
  await expect(panel.getByText('No active sessions found')).toBeHidden();
  unavailable = false;
  await panel.getByRole('button', { name: 'Retry' }).click();
  await expect(panel.getByRole('link', { name: /Fix the login bug/ })).toBeVisible();
});

test('clearing an unmatched project search keeps the owner and other URL context', async ({ mockedPage: page }) => {
  await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}?remoteId=local&context=keep&t=0&a=1&q=zzzzzzzzzz`);
  const panel = page.getByRole('tabpanel', { name: 'Sessions' });
  await expect(panel.getByRole('heading', { name: 'No matching sessions' })).toBeVisible();
  await panel.getByRole('button', { name: 'Clear filters' }).click();
  await expect(page).toHaveURL(/\?remoteId=local&context=keep$/);
  await expect(panel.getByRole('link', { name: /Fix the login bug/ })).toBeVisible();
});

for (const width of [1280, 390]) {
  test(`project settings share the owner defaults editor at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 844 });
    let defaults = { model: 'historical/model', agent: '', worktree: 'current', permissionMode: '' };
    const models = ['historical/model'];
    const writes: unknown[] = [];
    await page.route('/api/projects', route => route.fulfill({ json: [MOCK_PROJECT, { ...MOCK_PROJECT, remoteId: 'B', remoteName: 'Build machine' }] }));
    await page.route('/api/project/settings*', route => {
      if (route.request().method() === 'GET') {
        expect(new URL(route.request().url()).searchParams.get('remoteId')).toBe('B');
        return route.fulfill({ json: { models, off: false, defaults } });
      }
      const payload = route.request().postDataJSON();
      expect(payload.remoteId).toBe('B');
      expect(payload.defaults).toBeDefined();
      expect(payload.models).toBeUndefined();
      writes.push(payload);
      if (payload.defaults) defaults = payload.defaults;
      return route.fulfill({ json: { ok: true } });
    });
    await page.route('/api/sessions/prepare', route => {
      expect(route.request().postDataJSON().remoteId).toBe('B');
      return route.fulfill({ json: { agents: [{ name: 'build', mode: 'primary' }], models: { hasProviders: true, models: [{ provider: 'openai', providerName: 'OpenAI', model: 'gpt-5', modelName: 'GPT-5' }] } } });
    });
    await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}/settings?remoteId=B&q=login&t=0&a=1`);
    const table = page.getByRole('table', { name: 'Project settings' });
    await expect(page.getByRole('banner').getByTitle(MOCK_PROJECT.directory)).toHaveCount(1);
    await expect(page.getByRole('banner').getByLabel('Build machine', { exact: true })).toHaveCSS('border-radius', '999px');
    await expect(page.getByRole('note')).toContainText('Fallback model lists are shared by project path');
    await expect(page.getByRole('button', { name: 'Remove historical/model', exact: true })).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Clear list', exact: true })).toBeDisabled();
    await expect(page.getByRole('checkbox', { name: 'Disable fallthrough', exact: true })).toBeDisabled();
    await expect(page.getByRole('combobox', { name: 'Add model', exact: true })).toBeDisabled();
    await expect(table.getByRole('row', { name: /Default model/ })).toContainText('historical/model');
    await page.getByRole('button', { name: 'Edit project defaults' }).click();
    const dialog = page.getByRole('dialog', { name: 'Project quick settings' });
    await dialog.getByRole('combobox', { name: 'Default model', exact: true }).click();
    await expect(dialog.getByRole('option', { name: 'historical/model' })).toBeVisible();
    await dialog.getByRole('option', { name: 'openai/gpt-5', exact: true }).click();
    const save = dialog.getByRole('button', { name: 'Save', exact: true });
    await save.scrollIntoViewIfNeeded();
    const saveBox = (await save.boundingBox())!;
    expect(saveBox.x + saveBox.width).toBeLessThanOrEqual(width);
    expect(saveBox.y + saveBox.height).toBeLessThanOrEqual(844);
    await save.click();
    await expect(dialog).toBeHidden();
    await expect(table.getByRole('row', { name: /Default model/ })).toContainText('openai/gpt-5');
    expect(writes).toHaveLength(1);
    await table.locator('..').evaluate((element) => { element.scrollLeft = 0; });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    await page.screenshot({ path: testInfo.outputPath('project-settings.png') });
    await page.getByRole('tab', { name: 'Sessions', exact: true }).click();
    await expect(page.getByRole('searchbox', { name: 'Search sessions' })).toHaveValue('login');
    await page.goBack();
    await expect(page.getByRole('tab', { name: 'Settings', exact: true })).toHaveAttribute('aria-selected', 'true');
    await page.getByRole('button', { name: 'Project quick settings', exact: true }).click();
    await expect(page.getByRole('dialog', { name: 'Project quick settings' }).getByRole('combobox', { name: 'Default model', exact: true })).toHaveText('openai/gpt-5');
  });
  test(`project session filters survive refresh at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}?remoteId=local&t=0&a=1&q=login`);
    await expect(page.getByRole('tab', { name: 'Sessions', exact: true })).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByRole('button', { name: 'Project quick settings' })).toBeVisible();
    await expect(page.getByRole('banner').getByTitle(MOCK_PROJECT.directory)).toHaveCount(1);
    await expect(page.getByRole('banner').getByLabel('Local', { exact: true })).toHaveCSS('border-radius', '999px');
    await expect(page.getByRole('searchbox', { name: 'Search sessions' })).toHaveValue('login');
    await expect(page.getByRole('button', { name: 'Show archived' })).toHaveAttribute('aria-pressed', 'true');
    await page.reload();
    await expect(page.getByRole('searchbox', { name: 'Search sessions' })).toHaveValue('login');
    await expect(page.getByRole('button', { name: 'Show archived' })).toHaveAttribute('aria-pressed', 'true');
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    await page.screenshot({ path: testInfo.outputPath('project-sessions.png') });
  });
}
