import { test, expect, mockSessionWithLiveConnection, makeCapabilitiesWithPort } from './fixtures';

// A new conversation is a route until its first prompt: switching machines
// only re-points it (keeping the draft), and the prompt creates the session
// on the selected machine and target in one request.
test('machine selection re-points the new conversation and the first prompt creates it there', async ({ mockedPage: page }) => {
  const source = mockSessionWithLiveConnection();
  const target = { remoteId: 'box', remoteName: 'Build box', platform: 'r-box:opencode', dir: '/different/checkout' };
  const created = { ...source, id: 'remote-created', directory: target.dir, platform: target.platform, remoteId: target.remoteId };
  const capabilities = makeCapabilitiesWithPort();
  capabilities.platforms.push({ ...capabilities.platforms[0], id: target.platform });
  capabilities.hosts.push({ ...capabilities.hosts[0], remoteId: target.remoteId, remoteName: target.remoteName });
  await page.route('**/api/capabilities', (route) => route.fulfill({ json: capabilities }));
  await page.route(new RegExp(`/api/session/${created.id}(\\?|$)`), (route) => route.fulfill({ json: {
    session: created, messages: [], parts: [], totalMessages: 0, defaultAgent: 'build', defaultModel: 'anthropic/claude-3-5-sonnet-20241022',
  } }));
  await page.route('**/api/sessions/resolve-targets', (route) => route.fulfill({ json: {
    candidates: [target, { remoteId: 'local', remoteName: 'This machine', platform: source.platform, dir: source.directory }],
    remotes: [target, { remoteId: 'other', remoteName: 'Other', platform: 'r-other:opencode', dir: '' }],
  } }));
  await page.route('**/api/sessions/prepare', (route) => route.fulfill({ json: {
    platform: JSON.parse(route.request().postData() ?? '{}').platform || source.platform,
    agents: [], commands: [], models: { hasProviders: true, models: [] }, liveConnection: true,
  } }));
  await page.route('**/api/git/info*', (route) => route.fulfill({ json: {} }));
  await page.route('**/api/worktree/list*', (route) => route.fulfill({ json: { worktrees: [] } }));
  await page.route('**/api/sessions/start', (route) => route.fulfill({ json: {
    sessionId: created.id, platform: created.platform, remoteId: created.remoteId, directory: created.directory, firstMessageSent: true, firstMessageError: '',
  } }));

  await page.goto(`/session/new?dir=${encodeURIComponent(source.directory)}&platform=${encodeURIComponent(source.platform)}`);
  const machine = page.getByRole('combobox', { name: 'Session machine' });
  await expect(machine).toHaveText('This machine');
  await machine.click();
  await expect(page.getByRole('option', { name: 'Other · no matching project' })).toBeDisabled();
  await machine.click();
  const composer = page.getByRole('textbox');
  await composer.fill('Continue on the build box');
  await machine.click();
  await page.getByRole('option', { name: 'Build box' }).click();
  // No session was created by the switch; the route now names the machine.
  await expect(page).toHaveURL(/\/session\/new\?.*remoteId=box/);
  await expect(composer).toHaveValue('Continue on the build box');
  await expect(machine).toHaveText('Build box');
  const start = page.waitForRequest((request) => request.url().endsWith('/api/sessions/start') && request.method() === 'POST');
  await composer.press('Enter');
  const body = (await start).postDataJSON();
  expect(body).toMatchObject({ directory: target.dir, platform: target.platform, worktree: false, send: { message: 'Continue on the build box', agent: 'build' } });
  await expect(page).toHaveURL(/\/session\/remote-created/);
});
