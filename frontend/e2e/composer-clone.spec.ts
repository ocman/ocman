import { test, expect, mockSessionWithLiveConnection, makeCapabilitiesWithPort } from './fixtures';

for (const width of [1280, 390]) test(`clone confirmation, reload and first submission at ${width}px`, async ({ mockedPage: page }) => {
  await page.setViewportSize({ width, height: 844 });
  const source = mockSessionWithLiveConnection();
  const target = { remoteId: 'clone-box', remoteName: 'Build box', platform: 'r-clone-box:opencode', dir: '' };
  const url = 'https://example.com/Org/Repo.git';
  const destination = '/chosen/repo';
  const capabilities = makeCapabilitiesWithPort();
  capabilities.platforms.push({ ...capabilities.platforms[0], id: target.platform });
  capabilities.hosts.push({ ...capabilities.hosts[0], remoteId: target.remoteId, remoteName: target.remoteName });
  await page.route('**/api/capabilities', (route) => route.fulfill({ json: capabilities }));
  await page.route('**/api/sessions/resolve-targets', (route) => route.fulfill({ json: {
    candidates: [{ remoteId: 'local', remoteName: 'This machine', platform: source.platform, dir: source.directory }], remotes: [target], cloneUrl: url,
  } }));
  await page.route('**/api/sessions/plan-clone', (route) => route.fulfill({ json: {
    url, directory: route.request().postDataJSON().directory || '/remote/src/repo',
  } }));
  await page.route('**/api/sessions/prepare', (route) => route.fulfill({ json: {
    platform: route.request().postDataJSON().platform || source.platform,
    defaultAgent: 'build', agents: [{ name: 'build' }], commands: [], models: { hasProviders: true, models: [] }, liveConnection: true,
  } }));
  await page.route('**/api/git/info*', (route) => route.fulfill({ json: {} }));
  await page.route('**/api/worktree/list*', (route) => route.fulfill({ json: { worktrees: [] } }));
  const starts: unknown[] = [];
  await page.route('**/api/sessions/start', (route) => {
    starts.push(route.request().postDataJSON());
    return starts.length === 1
      ? route.fulfill({ status: 502, body: 'Clone failed: check remote credentials' })
      : route.fulfill({ json: { sessionId: 'cloned-session', platform: target.platform, remoteId: target.remoteId, directory: destination, firstMessageSent: true, firstMessageError: '' } });
  });
  await page.route(/\/api\/session\/cloned-session(\?|$)/, (route) => route.fulfill({ json: {
    session: { ...source, id: 'cloned-session', directory: destination, platform: target.platform, remoteId: target.remoteId }, messages: [], parts: [], totalMessages: 0,
  } }));

  await page.goto(`/session/new?dir=${encodeURIComponent(source.directory)}&platform=${source.platform}`);
  const composer = page.getByTestId('conversation-composer').getByRole('textbox');
  await composer.fill('Continue on the build box');
  await page.getByRole('combobox', { name: 'Session machine' }).click();
  await page.getByRole('option', { name: 'Build box · no matching project' }).click();
  const dialog = page.getByRole('dialog', { name: 'Clone project on machine' });
  await expect(dialog).toContainText('Local uncommitted changes and unpushed commits will not transfer');
  await expect(dialog.getByLabel('Destination directory')).toHaveValue('/remote/src/repo');
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page).not.toHaveURL(/remoteId=clone-box/);
  expect(starts).toHaveLength(0);

  await page.getByRole('combobox', { name: 'Session machine' }).click();
  await page.getByRole('option', { name: 'Build box · no matching project' }).click();
  await expect(dialog.getByLabel('Destination directory')).toHaveValue('/remote/src/repo');
  await dialog.getByLabel('Destination directory').fill(destination);
  await dialog.getByRole('button', { name: 'Use machine and clone on submit' }).click();
  await expect(page).toHaveURL(/remoteId=clone-box/);
  await expect(composer).toHaveValue('Continue on the build box');
  expect(page.url()).not.toContain('cloneUrl');
  expect(starts).toHaveLength(0);
  await page.reload();
  await expect(composer).toHaveValue('Continue on the build box');
  await expect(page.getByText(`On first submission, clone ${url}`, { exact: false })).toBeVisible();
  await composer.press('Enter');
  await expect(page.getByRole('alert').filter({ hasText: 'Clone failed' }).first()).toBeVisible();
  await expect(composer).toHaveValue('Continue on the build box');
  expect(starts).toHaveLength(1);
  await composer.press('Enter');
  await expect(page).toHaveURL(/\/session\/cloned-session$/);
  expect(starts).toHaveLength(2);
  for (const start of starts) expect(start).toMatchObject({ directory: destination, remoteId: target.remoteId, platform: target.platform, cloneUrl: url, worktree: false, send: { message: 'Continue on the build box' } });
});
