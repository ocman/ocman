import { test, expect } from './fixtures';

test('seven settings categories contain named sections and keep legacy links working', async ({ mockedPage: page }) => {
  await page.route('/api/settings/platforms', (route) => route.fulfill({ json: { enabled: ['opencode', 'claudecode'] } }));
  await page.route('/api/settings/model-fallthrough', (route) => route.fulfill({ json: { patienceMinutes: 15, fallbackMinutes: 60 } }));
  await page.route('/api/settings/judge-endpoint', (route) => route.fulfill({ json: { format: '', endpoint: '', model: '', minSafeProbability: .99, apiKeySet: false } }));
  await page.setViewportSize({ width: 1280, height: 1200 });
  await page.goto('/settings?tab=auto-approve');
  const navigation = page.getByRole('navigation', { name: 'Settings groups' });
  await expect(navigation.getByRole('button')).toHaveText([
    'App & notifications', 'Sessions', 'Agents & models', 'Permissions', 'Connections & sharing', 'Plugins', 'System',
  ]);
  for (const name of ['Auto-approve', 'AI reviewer', 'Worktrees']) {
    await expect(page.getByRole('heading', { name, level: 3, exact: true })).toBeVisible();
  }
  await page.screenshot({ path: test.info().outputPath('settings-desktop.png'), fullPage: true, animations: 'disabled' });
  await navigation.getByRole('button', { name: 'Sessions', exact: true }).click();
  for (const name of ['New sessions', 'Conversation', 'Voice', 'History & archiving']) {
    await expect(page.getByRole('heading', { name, level: 3, exact: true })).toBeVisible();
  }
  const search = page.getByRole('searchbox', { name: 'Search settings' });
  await search.fill('Model fallthrough patience');
  await page.getByRole('button', { name: /Model fallthrough patience/ }).click();
  await expect(page.getByRole('heading', { name: 'Agents & models', level: 2 })).toBeVisible();
  await expect(page.getByRole('spinbutton', { name: 'Model fallthrough patience in minutes' })).toBeVisible();
  await search.fill('Worktree sessions inherit parent permissions');
  await page.getByRole('button', { name: /Worktree sessions inherit parent permissions/ }).click();
  await expect(page.getByRole('checkbox', { name: 'Worktree sessions inherit parent permissions' })).toBeVisible();
  await search.focus();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole('combobox', { name: 'Settings group', exact: true })).toHaveValue('permissions');
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await page.screenshot({ path: test.info().outputPath('settings-mobile.png'), fullPage: true, animations: 'disabled' });
});

test('reviewer edits survive switching settings categories', async ({ mockedPage: page }) => {
  await page.goto('/settings?tab=permissions');
  await page.getByRole('button', { name: '+ Add section', exact: true }).click();
  const title = page.getByRole('textbox', { name: 'Section 1 title' });
  await title.fill('Project safety');
  const navigation = page.getByRole('navigation', { name: 'Settings groups' });
  await navigation.getByRole('button', { name: 'Sessions', exact: true }).click();
  await expect(title).toBeHidden();
  await navigation.getByRole('button', { name: 'Permissions', exact: true }).click();
  await expect(title).toHaveValue('Project safety');
});

for (const width of [1280, 390]) {
  test(`platform selection is available in Settings at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    let enabled = ['opencode', 'claudecode'];
    await page.route('/api/settings/platforms', (route) => {
      if (route.request().method() === 'POST') enabled = route.request().postDataJSON().enabled;
      return route.fulfill({ json: { enabled } });
    });
    const openPlatforms = async () => {
      await page.goto('/settings');
      if (width === 390) await page.getByRole('combobox', { name: 'Settings group', exact: true }).selectOption('agents');
      else await page.getByRole('button', { name: 'Agents & models', exact: true }).click();
    };
    await openPlatforms();
    const openCode = page.getByRole('checkbox', { name: 'OpenCode', exact: true });
    const claude = page.getByRole('checkbox', { name: 'Claude Code', exact: true });
    await expect(openCode).toBeChecked();
    await expect(claude).toBeChecked();
    await openCode.uncheck();
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(page.getByText('Platform changes applied.', { exact: true })).toBeVisible();
    expect(enabled).toEqual(['claudecode']);
    await openPlatforms();
    await expect(openCode).not.toBeChecked();
    await expect(claude).toBeChecked();
  });
  test(`Remotes table scrolls inside Settings at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto('/settings');
    if (width === 390) await page.getByRole('combobox', { name: 'Settings group', exact: true }).selectOption('connections');
    else await page.getByRole('button', { name: 'Connections & sharing', exact: true }).click();
    const settings = page.getByTestId('remote-settings');
    await expect(settings).toBeVisible();
    const bounds = await settings.boundingBox();
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width);
    expect(bounds!.width).toBeLessThanOrEqual(640);
    const frame = page.getByRole('table', { name: 'Machines' }).locator('..');
    expect(await frame.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(true);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
  });
  test(`settings group navigation and search share the same selection at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.route('/api/settings/prompt-sections', route => route.fulfill({ json: [{ title: 'Project safety', content: 'Review commands before approving changes outside this project.' }] }));
    await page.goto('/settings');
    const groups = page.getByRole('combobox', { name: 'Settings group', exact: true });
    const search = page.getByRole('searchbox', { name: 'Search settings' });
    await expect(search).toBeVisible();
    if (width === 390) {
      await expect(groups).toBeVisible();
      await expect(page.getByRole('button', { name: 'Permissions', exact: true })).toHaveCount(0);
      await expect(groups.getByRole('option', { name: 'Account', exact: true })).toHaveCount(0);
      await groups.selectOption('permissions');
    } else {
      await expect(groups).toBeHidden();
      await page.getByRole('button', { name: 'Permissions', exact: true }).click();
    }
    await expect(page.getByRole('textbox', { name: 'Section 1 title' })).toHaveValue('Project safety');
    const content = page.getByRole('textbox', { name: 'Section 1 instructions' });
    const bounds = await content.boundingBox();
    expect(bounds!.width).toBeGreaterThan(width === 390 ? 300 : 500);
    await search.fill('Bell sound');
    await page.getByRole('button', { name: /App & notifications Bell sound/ }).click();
    await expect(search).toHaveValue('');
    await expect(page.getByRole('checkbox', { name: 'Bell sound', exact: true })).toBeVisible();
    if (width === 390) await expect(groups).toHaveValue('app');
    else await expect(page.getByRole('button', { name: 'App & notifications', exact: true })).toHaveAttribute('aria-current', 'page');
    await search.fill('disk space');
    if (width === 390) await groups.selectOption('sessions');
    else await page.getByRole('button', { name: 'Sessions', exact: true }).click();
    await expect(search).toHaveValue('');
    await expect(page.getByRole('heading', { name: 'Sessions', exact: true })).toBeVisible();
  });
}
