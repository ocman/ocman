import { test, expect, MOCK_SESSION, mockSessionWithLiveConnection, makeCapabilitiesWithPort } from './fixtures';

test('machine selection creates in the matching checkout and keeps the draft', async ({ mockedPage: page }) => {
  const source = { ...mockSessionWithLiveConnection(), messageCount: 0 };
  const target = { remoteId: 'box', remoteName: 'Build box', platform: 'r-box:opencode', dir: '/different/checkout' };
  const destination = { ...source, id: 'remote-created', directory: target.dir, platform: target.platform, remoteId: target.remoteId };
  const capabilities = makeCapabilitiesWithPort();
  capabilities.platforms.push({ ...capabilities.platforms[0], id: target.platform });
  capabilities.hosts.push({ ...capabilities.hosts[0], remoteId: target.remoteId, remoteName: target.remoteName });
  await page.route('**/api/capabilities', (route) => route.fulfill({ json: capabilities }));
  for (const session of [source, destination]) {
    await page.route(new RegExp(`/api/session/${session.id}(\\?|$)`), (route) => route.fulfill({ json: {
      session, messages: [], parts: [], totalMessages: 0, defaultAgent: 'build', defaultModel: 'anthropic/claude-3-5-sonnet-20241022',
    } }));
  }
  await page.route('**/api/sessions/resolve-targets', (route) => route.fulfill({ json: {
    candidates: [target, { remoteId: 'local', remoteName: 'This machine', platform: source.platform, dir: source.directory }],
    remotes: [target, { remoteId: 'other', remoteName: 'Other', platform: 'r-other:opencode', dir: '' }],
  } }));
  await page.route('**/api/sessions', (route) => route.request().method() === 'POST'
    ? route.fulfill({ json: { id: destination.id } }) : route.fallback());
  await page.route(`**/api/session/${destination.id}/message*`, (route) => route.fulfill({ status: 204 }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const machine = page.getByRole('combobox', { name: 'Session machine' });
  await expect(machine).toHaveValue('local');
  await expect(machine.getByRole('option', { name: 'Other · no matching project' })).toBeDisabled();
  const composer = page.getByRole('textbox');
  await composer.fill('Continue on the build box');
  const creation = page.waitForRequest((request) => request.url().endsWith('/api/sessions') && request.method() === 'POST');
  await machine.selectOption('box');
  expect((await creation).postDataJSON()).toEqual({ directory: target.dir, platform: target.platform });
  await expect(page).toHaveURL(/\/session\/remote-created/);
  await expect(composer).toHaveValue('Continue on the build box');
  await expect(machine).toHaveValue('box');
  const send = page.waitForRequest((request) => request.url().includes('/remote-created/message') && request.method() === 'POST');
  await composer.press('Enter');
  const request = await send;
  expect(new URL(request.url()).searchParams.get('platform')).toBe(target.platform);
  expect(request.postDataJSON().message).toBe('Continue on the build box');
});
