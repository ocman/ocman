import { test, expect } from './fixtures';

for (const trigger of ['navigation', 'shortcut']) test(`create a project-free draft through ${trigger}`, async ({ mockedPage: page }) => {
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
  await page.goto('/sessions');
  await expect(page.getByRole('button', { name: 'New one-off session' })).toBeVisible();
  if (trigger === 'shortcut') await page.keyboard.press('Alt+o');
  else await page.getByRole('button', { name: 'New one-off session' }).click();
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

test('allocation errors remain visible outside the phone navigation', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 390, height: 720 });
  await page.route('**/api/sessions/one-off', route => route.fulfill({ status: 503, body: 'Machine is disconnected' }));
  await page.goto('/sessions');
  await page.getByRole('button', { name: 'More', exact: true }).click();
  await page.getByRole('button', { name: 'New one-off session' }).click();
  const dialog = page.getByRole('dialog', { name: 'Could not create one-off session' });
  await expect(dialog).toBeInViewport();
  await expect(dialog.getByRole('alert')).toHaveText('Machine is disconnected');
  expect(await dialog.evaluate(node => node.closest('.main-nav'))).toBeNull();
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog).not.toBeVisible();
});
