import { test, expect, mockSessionWithLiveConnection, makeCapabilitiesWithPort } from './fixtures';
import { mockDraftRoutes } from './draft-routes';

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
  let sessionId = '';
  await mockDraftRoutes(page, (request, id) => {
    starts.push(request);
    if (starts.length === 1) throw new Error('Clone failed: check remote credentials');
    sessionId = id;
    return { sessionId: id, platform: target.platform, remoteId: target.remoteId, directory: destination, firstMessageSent: true, firstMessageError: '' };
  });
  await page.route(/\/api\/session\/ocm_[^/?]+(\?|$)/, (route) => route.fulfill({ json: {
    session: { ...source, id: sessionId, directory: destination, platform: target.platform, remoteId: target.remoteId }, messages: [], parts: [], totalMessages: 0,
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
  const draftId = new URL(page.url()).searchParams.get('draftId')!;
  await expect(composer).toHaveValue('Continue on the build box');
  await expect(page.getByText(`On first submission, clone ${url}`, { exact: false })).toBeVisible();
  await composer.press('Enter');
  await expect(page.getByRole('alert').filter({ hasText: 'Clone failed' }).first()).toBeVisible();
  await expect(composer).toHaveValue('Continue on the build box');
  expect(starts).toHaveLength(1);
  await composer.press('Enter');
  await expect(page).toHaveURL(new RegExp(`/session/${draftId}$`));
  expect(starts).toHaveLength(2);
  for (const start of starts) expect(start).toMatchObject({ directory: destination, remoteId: target.remoteId, platform: target.platform, cloneUrl: url, worktree: false, send: { message: 'Continue on the build box' } });
});

test('browser Back submits from the original checkout without stale clone consent', async ({ mockedPage: page }) => {
  const source = mockSessionWithLiveConnection();
  const target = { remoteId: 'history-box', remoteName: 'History box', platform: 'r-history-box:opencode', dir: '' };
  const capabilities = makeCapabilitiesWithPort();
  capabilities.platforms.push({ ...capabilities.platforms[0], id: target.platform });
  capabilities.hosts.push({ ...capabilities.hosts[0], remoteId: target.remoteId, remoteName: target.remoteName });
  await page.route('**/api/capabilities', (route) => route.fulfill({ json: capabilities }));
  await page.route('**/api/sessions/resolve-targets', (route) => route.fulfill({ json: {
    candidates: [{ remoteId: 'local', remoteName: 'This machine', platform: source.platform, dir: source.directory }], remotes: [target], cloneUrl: 'https://example.com/repo.git',
  } }));
  await page.route('**/api/sessions/plan-clone', (route) => route.fulfill({ json: { url: 'https://example.com/repo.git', directory: '/new/repo' } }));
  await page.route('**/api/sessions/prepare', (route) => route.fulfill({ json: {
    platform: route.request().postDataJSON().platform || source.platform,
    defaultAgent: 'build', agents: [{ name: 'build' }], commands: [], models: { hasProviders: true, models: [] }, liveConnection: true,
  } }));
  await page.route('**/api/git/info*', (route) => route.fulfill({ json: {} }));
  await page.route('**/api/worktree/list*', (route) => route.fulfill({ json: { worktrees: [] } }));
  await page.route('**/api/sessions/start', (route) => route.fulfill({ json: {
    sessionId: 'history-created', platform: source.platform, remoteId: 'local', directory: source.directory, firstMessageSent: true, firstMessageError: '',
  } }));
  await page.route(/\/api\/session\/history-created(\?|$)/, (route) => route.fulfill({ json: {
    session: { ...source, id: 'history-created' }, messages: [], parts: [], totalMessages: 0,
  } }));
  await page.goto(`/session/new?dir=${encodeURIComponent(source.directory)}&platform=${source.platform}&draftId=clone-history`);
  const composer = page.getByTestId('conversation-composer').getByRole('textbox');
  await composer.fill('Keep working in the original checkout');
  await page.getByRole('combobox', { name: 'Session machine' }).click();
  await page.getByRole('option', { name: 'History box · no matching project' }).click();
  await expect(page.getByLabel('Destination directory')).toHaveValue('/new/repo');
  await page.getByRole('button', { name: 'Use machine and clone on submit' }).click();
  await expect(page).toHaveURL(/remoteId=history-box/);
  await page.goBack();
  await expect(page).not.toHaveURL(/remoteId=history-box/);
  await expect(page.getByText('On first submission, clone', { exact: false })).not.toBeVisible();
  await expect(composer).toHaveValue('Keep working in the original checkout');
  const start = page.waitForRequest((request) => request.url().endsWith('/api/sessions/start') && request.method() === 'POST');
  await composer.press('Enter');
  const request = (await start).postDataJSON();
  expect(request).toMatchObject({ directory: source.directory, remoteId: 'local', send: { message: 'Keep working in the original checkout' } });
  expect(request).not.toHaveProperty('cloneUrl');
  await expect(page).toHaveURL(/\/session\/history-created$/);
});
