import { test, expect, MOCK_PROJECT } from './fixtures';

for (const width of [1280, 390]) {
  test(`project model actions preserve order, pending protection and rollback at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    const first = 'anthropic/claude-haiku-4-5';
    const second = 'openai/gpt-5';
    let models = [first, second];
    let off = true;
    const writes: { directory: string; models: string[]; off: boolean }[] = [];
    let finish!: () => void;
    const pending = new Promise<void>(resolve => { finish = resolve; });
    await page.route('/api/project/settings*', async route => {
      if (route.request().method() === 'GET') return route.fulfill({ json: { models, off } });
      const payload = route.request().postDataJSON();
      writes.push(payload);
      if (writes.length === 1) await pending;
      if (writes.length === 2) return route.fulfill({ status: 400, contentType: 'text/plain', body: 'Save rejected. Previous model order restored.' });
      models = payload.models;
      off = payload.off;
      return route.fulfill({ status: 204 });
    });
    await page.route('/api/session/*/models*', route => route.fulfill({ json: { hasProviders: true, models: [
      { provider: 'anthropic', model: 'claude-haiku-4-5', providerName: 'Anthropic', modelName: 'Claude Haiku' },
      { provider: 'openai', model: 'gpt-5', providerName: 'OpenAI', modelName: 'GPT-5' },
    ] } }));
    await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}/settings`);
    const list = page.getByRole('list', { name: 'Project models' });
    await expect(list.getByRole('listitem')).toHaveCount(2);
    await expect(list.getByRole('listitem').first()).toContainText(first);
    await page.getByRole('button', { name: `Move ${second} up`, exact: true }).click();
    await expect(page.getByRole('button', { name: 'Clear list', exact: true })).toBeDisabled();
    await expect(page.getByRole('combobox', { name: 'Add model', exact: true })).toBeDisabled();
    await expect(page.getByRole('checkbox', { name: 'Disable fallthrough', exact: true })).toBeDisabled();
    await expect.poll(() => writes.length).toBe(1);
    expect(writes[0]).toEqual({ directory: MOCK_PROJECT.directory, models: [second, first], off: true });
    finish();
    await expect(page.getByRole('button', { name: 'Clear list', exact: true })).toBeEnabled();
    await expect(list.getByRole('listitem').first()).toContainText(second);
    await page.getByRole('button', { name: `Remove ${second}`, exact: true }).click();
    await expect(page.getByRole('alert')).toContainText('Save rejected');
    await expect(list.getByRole('listitem')).toHaveCount(2);
    await expect(list.getByRole('listitem').first()).toContainText(second);
    await page.getByRole('button', { name: 'Clear list', exact: true }).click();
    await expect.poll(() => writes.length).toBe(3);
    await expect(page.getByRole('combobox', { name: 'Add model', exact: true })).toBeEnabled();
    await expect(page.getByRole('alert')).toHaveCount(0);
    await expect(page.getByTestId('project-models-empty')).toBeVisible();
    await expect(page.getByRole('checkbox', { name: 'Disable fallthrough', exact: true })).toBeDisabled();
    expect(writes[2]).toEqual({ directory: MOCK_PROJECT.directory, models: [], off: true });
  });
}
