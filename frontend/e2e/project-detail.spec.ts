import { test, expect, MOCK_PROJECT } from './fixtures';

for (const width of [1280, 390]) {
  test(`project settings share the owner defaults editor at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 844 });
    let defaults = { model: 'historical/model', agent: '', worktree: 'current', permissionMode: '' };
    let models = ['historical/model'];
    const writes: unknown[] = [];
    await page.route('/api/project/settings*', route => {
      if (route.request().method() === 'GET') {
        expect(new URL(route.request().url()).searchParams.get('remoteId')).toBe('B');
        return route.fulfill({ json: { models, off: false, defaults } });
      }
      const payload = route.request().postDataJSON();
      expect(payload.remoteId).toBe('B');
      writes.push(payload);
      if (payload.defaults) defaults = payload.defaults;
      if (payload.models) models = payload.models;
      return route.fulfill({ json: { ok: true } });
    });
    await page.route('/api/sessions/prepare', route => {
      expect(route.request().postDataJSON().remoteId).toBe('B');
      return route.fulfill({ json: { agents: [{ name: 'build', mode: 'primary' }], models: { hasProviders: true, models: [{ provider: 'openai', providerName: 'OpenAI', model: 'gpt-5', modelName: 'GPT-5' }] } } });
    });
    await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}/settings?remoteId=B&q=login&t=0&a=1`);
    const table = page.getByRole('table', { name: 'Project settings' });
    await expect(page.getByTitle(MOCK_PROJECT.directory)).toHaveCount(1);
    await expect(page.getByRole('banner').getByLabel('B', { exact: true })).toHaveCSS('border-radius', '999px');
    await expect(table.getByRole('row', { name: /Default model/ })).toContainText('historical/model');
    await page.getByRole('button', { name: 'Edit project defaults' }).click();
    const dialog = page.getByRole('dialog', { name: 'Project quick settings' });
    await dialog.getByRole('combobox', { name: 'Default model', exact: true }).click();
    await expect(dialog.getByRole('option', { name: 'historical/model' })).toBeVisible();
    await dialog.getByRole('option', { name: 'OpenAI / GPT-5', exact: true }).click();
    await dialog.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect(table.getByRole('row', { name: /Default model/ })).toContainText('openai/gpt-5');
    expect(writes).toHaveLength(1);
    await page.getByRole('combobox', { name: 'Add model', exact: true }).click();
    await page.getByRole('option', { name: 'OpenAI / GPT-5', exact: true }).click();
    await expect(page.getByRole('list', { name: 'Project models' })).toContainText('openai/gpt-5');
    await expect(page.getByRole('combobox', { name: 'Add model', exact: true })).toBeEnabled();
    expect(writes).toHaveLength(2);
    await table.locator('..').evaluate((element) => { element.scrollLeft = 0; });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    await page.screenshot({ path: testInfo.outputPath('project-settings.png') });
    await page.getByRole('tab', { name: 'Sessions', exact: true }).click();
    await expect(page.getByRole('searchbox', { name: 'Search sessions' })).toHaveValue('login');
    await page.goBack();
    await expect(page.getByRole('tab', { name: 'Settings', exact: true })).toHaveAttribute('aria-selected', 'true');
    await page.getByRole('button', { name: 'Project quick settings', exact: true }).click();
    await expect(page.getByRole('dialog', { name: 'Project quick settings' }).getByRole('combobox', { name: 'Default model', exact: true })).toHaveText('OpenAI / GPT-5');
  });
  test(`project session filters survive refresh at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}?remoteId=local&t=0&a=1&q=login`);
    await expect(page.getByRole('tab', { name: 'Sessions', exact: true })).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByRole('button', { name: 'Project quick settings' })).toBeVisible();
    await expect(page.getByTitle(MOCK_PROJECT.directory)).toHaveCount(1);
    await expect(page.getByRole('banner').getByLabel('Local', { exact: true })).toHaveCSS('border-radius', '999px');
    await expect(page.getByRole('searchbox', { name: 'Search sessions' })).toHaveValue('login');
    await expect(page.getByRole('button', { name: 'Exclude archived' })).toHaveAttribute('aria-pressed', 'true');
    await page.reload();
    await expect(page.getByRole('searchbox', { name: 'Search sessions' })).toHaveValue('login');
    await expect(page.getByRole('button', { name: 'Exclude archived' })).toHaveAttribute('aria-pressed', 'true');
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    await page.screenshot({ path: testInfo.outputPath('project-sessions.png') });
  });
}
