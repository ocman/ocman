import { test, expect } from './fixtures';

for (const trigger of ['project picker', 'shortcut']) test(`create a project-free draft through ${trigger}`, async ({ mockedPage: page }) => {
  const directory = '/tmp/ocman-one-off-example';
  let allocations = 0;
  let starts = 0;
  await page.route('**/api/projects*', route => route.fulfill({ json: [] }));
  await page.route('**/api/sessions/one-off', route => {
    expect(route.request().postDataJSON()).toEqual({ remoteId: 'local' });
    allocations++;
    return route.fulfill({ json: { directory } });
  });
  await page.route('**/api/sessions/prepare', route => route.fulfill({ json: {
    platform: 'opencode', agents: [{ name: 'build' }], commands: [],
    models: { hasProviders: true, models: [] }, liveConnection: false,
  } }));
  await page.route('**/api/sessions/resolve-targets', route => route.fulfill({ json: {
    candidates: [{ remoteId: 'local', remoteName: 'This machine', platform: 'opencode', dir: directory }], remotes: [],
  } }));
  await page.route('**/api/sessions/start', route => { starts++; return route.fulfill({ status: 500 }); });
  await Promise.all([page.waitForResponse('**/api/keybindings'), page.goto('/sessions')]);
  await expect(page.getByRole('button', { name: 'New session', exact: true }).first()).toBeVisible();
  await expect(page.getByRole('button', { name: 'New one-off session' })).toHaveCount(0);
  if (trigger === 'shortcut') await page.keyboard.press('Alt+o');
  else {
    await page.getByRole('button', { name: 'New session', exact: true }).first().click();
    await page.getByRole('option').filter({ hasText: 'one-off' }).click();
  }
  await expect(page).toHaveURL(/\/session\/ocm_.*draftId=ocm_/);
  expect(new URL(page.url()).searchParams.get('dir')).toBe(directory);
  const composer = page.getByRole('textbox');
  const prompt = 'A quick question, without a project';
  await Promise.all([
    page.waitForResponse(response => response.url().includes('/api/session-drafts/') &&
      response.request().method() === 'PUT' && response.request().postDataJSON().text === prompt),
    composer.fill(prompt),
  ]);
  await page.reload();
  await expect(composer).toHaveValue(prompt);
  expect(allocations).toBe(1);
  expect(starts).toBe(0);
});

test('allocation errors remain visible in the project picker on phones', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 390, height: 720 });
  await page.route('**/api/sessions/one-off', route => route.fulfill({ status: 503, body: 'Machine is disconnected' }));
  await page.goto('/sessions');
  await page.getByRole('button', { name: 'New session', exact: true }).first().click();
  await page.getByRole('option').filter({ hasText: 'one-off' }).click();
  await expect(page.getByRole('alert')).toHaveText('Machine is disconnected');
  await expect(page.getByRole('listbox', { name: 'Results' })).toBeInViewport();
  await expect(page).toHaveURL(/\/sessions$/);
});
