import { test, expect, MOCK_SESSION } from './fixtures';

const ownedID = 'ocm_11111111-1111-4111-8111-111111111111';
const missingID = 'ocm_22222222-2222-4222-8222-222222222222';

test('legacy links resolve before native streams and preserve route context', async ({ mockedPage: page }) => {
  const session = { ...MOCK_SESSION, id: ownedID };
  const nativeRequests: string[] = [];
  page.on('request', (request) => {
    if (new URL(request.url()).pathname.startsWith(`/api/session/${MOCK_SESSION.id}`)) nativeRequests.push(request.url());
  });
  await page.route('/api/capabilities', (route) => route.fulfill({ json: {
    ownedSessionIds: true, hosts: [], worktreeSessions: false,
    platforms: [{ id: 'opencode', displayName: 'OpenCode', available: true, capabilities: { composer: true, events: true } }],
  } }));
  await page.route(/\/api\/sessions(?:\?|$)/, (route) => route.fulfill({ json: [session] }));
  await page.route('**/api/session-identity?**', (route) => route.fulfill({ json: {
    id: ownedID, nativeId: MOCK_SESSION.id, platform: 'opencode', ownerId: 'owner',
  } }));
  await page.route(new RegExp(`/api/session/${ownedID}(?:\\?|$)`), (route) => route.fulfill({ json: {
    session, messages: [], parts: [], sessionTree: [], totalMessages: 0,
  } }));

  await page.goto(`/session/${MOCK_SESSION.id}?platform=opencode&peek=1#message`);
  await expect(page).toHaveURL(new RegExp(`/session/${ownedID}\\?platform=opencode&peek=1#message$`));
  await expect(page.getByRole('heading', { name: MOCK_SESSION.title })).toBeVisible();
  expect(nativeRequests).toEqual([]);
});

test('unknown owned links never retry against a native session route', async ({ mockedPage: page }) => {
  const lookedUp: string[] = [];
  const detailRequests: string[] = [];
  page.on('request', (request) => {
    if (new URL(request.url()).pathname.startsWith('/api/session/')) detailRequests.push(new URL(request.url()).pathname);
  });
  await page.route('/api/capabilities', (route) => route.fulfill({ json: {
    ownedSessionIds: true, hosts: [], platforms: [{ id: 'opencode', displayName: 'OpenCode', available: true, capabilities: {} }],
  } }));
  await page.route('**/api/session-identity?**', (route) => {
    lookedUp.push(route.request().url());
    return route.fulfill({ status: 404, contentType: 'text/plain', body: 'Session identity not found' });
  });
  await page.route(`**/api/session/${missingID}**`, (route) => route.fulfill({ status: 404, contentType: 'text/plain', body: 'Session identity not found' }));
  await page.goto(`/session/${missingID}?platform=opencode`);
  await expect(page.getByRole('alert').filter({ hasText: 'Session identity not found' }).first()).toBeVisible();
  expect(lookedUp.length).toBeGreaterThan(0);
  expect(lookedUp.every((url) => new URL(url).searchParams.get('id') === missingID && !new URL(url).searchParams.has('nativeId'))).toBe(true);
  expect(detailRequests.every((path) => path.startsWith(`/api/session/${missingID}`))).toBe(true);
});
