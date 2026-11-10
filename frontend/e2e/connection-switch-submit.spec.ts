import { test, expect, MOCK_SESSION, makeCapabilitiesWithPort } from './fixtures';
import type { BackendConversationDraft } from '../src/lib/backendConversationDrafts';
import type { PlatformConnection } from '../src/lib/platformConnections';

test('cross-kind connection switch submits destination defaults and pins durable delivery retry', async ({ mockedPage: page }) => {
  const id = 'ocm_01234567-89ab-4cde-8fab-0123456789ab';
  const connections: PlatformConnection[] = [
    { id: 'cnx_codex', kind: 'codex', name: 'Codex Personal', configuration: 'existing', directory: '/codex', enabled: true, isDefault: true, status: 'ready' },
    { id: 'cnx_codex_work', kind: 'codex', name: 'Codex Work', configuration: 'isolated', directory: '/codex-work', enabled: true, isDefault: false, status: 'ready' },
    { id: 'cnx_claude', kind: 'claudecode', name: 'Claude Work', configuration: 'isolated', directory: '/claude', enabled: true, isDefault: false, status: 'ready' },
  ];
  let draft: BackendConversationDraft = { id, metadata: { draftId: id, directory: MOCK_SESSION.directory, remoteId: 'local', platform: 'cnx_codex',
    text: 'Keep my prompt', model: 'codex:openai/gpt', agent: 'builder', reasoning: 'high' }, phase: 'draft', createdAt: Date.now(), steps: {} };
  const starts: unknown[] = [];
  const retries: unknown[] = [];
  const capabilities = makeCapabilitiesWithPort();
  capabilities.platforms = connections.map(connection => ({ ...capabilities.platforms[0], id: connection.id, kind: connection.kind, displayName: connection.name }));
  await page.route('**/api/capabilities', route => route.fulfill({ json: capabilities }));
  await page.route('**/api/platform-connections**', route => route.fulfill({ json: { supported: true, onboardingComplete: true, platforms: [], connections } }));
  await page.route('**/api/git/info*', route => route.fulfill({ json: {} }));
  await page.route('**/api/worktree/list*', route => route.fulfill({ json: { worktrees: [] } }));
  await page.route('**/api/sessions/prepare', route => {
    const selected = route.request().postDataJSON().platform;
    const claude = selected === 'cnx_claude';
    const kind = claude ? 'claudecode' : 'codex';
    const provider = claude ? 'anthropic' : 'openai';
    const model = claude ? 'sonnet' : 'gpt';
    return route.fulfill({ json: { platform: selected, defaultModel: `${kind}:${provider}/${model}`, defaultAgent: claude ? 'assistant' : 'builder',
      agents: [{ name: claude ? 'assistant' : 'builder' }], commands: [], liveConnection: true,
      models: { hasProviders: true, models: [{ platform: kind, provider, model, name: model, isAvailable: true }] } } });
  });
  await page.route('**/api/session-drafts**', route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path.endsWith('/retry-delivery')) {
      retries.push(draft.request);
      draft = { ...draft, error: undefined, phase: 'delivering' };
      return route.fulfill({ status: 204 });
    }
    if (path.endsWith('/start')) {
      starts.push(request.postDataJSON());
      draft = { ...draft, request: request.postDataJSON(), submittedAt: Date.now(), phase: 'failed', error: 'Temporary delivery failure',
        result: { sessionId: id, platform: 'cnx_claude', directory: MOCK_SESSION.directory, remoteId: 'local', firstMessageSent: false, firstMessageError: 'Temporary delivery failure' } };
      return route.fulfill({ status: 202, json: draft });
    }
    if (request.method() === 'PUT') {
      draft = { ...draft, metadata: request.postDataJSON() };
      return route.fulfill({ status: 204 });
    }
    return route.fulfill({ json: path === '/api/session-drafts' ? [draft] : draft });
  });
  await page.goto(`/session/${id}?dir=${encodeURIComponent(MOCK_SESSION.directory)}&platform=cnx_codex&draftId=${id}`);
  const selector = page.getByRole('combobox', { name: 'Platform connection', exact: true });
  const input = page.getByRole('textbox');
  await expect(input).toHaveValue('Keep my prompt');
  await expect(page.getByRole('button', { name: 'gpt', exact: true })).toBeVisible();
  await page.locator('input[type="file"]').setInputFiles({ name: 'keep.png', mimeType: 'image/png', buffer: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l9sAAAAASUVORK5CYII=', 'base64') });
  await expect(page.getByRole('button', { name: 'Remove image attachment 1', exact: true })).toBeVisible();
  await selector.selectOption('cnx_codex_work');
  await expect(selector).toHaveValue('cnx_codex_work');
  await expect(page.getByRole('button', { name: 'gpt', exact: true })).toBeVisible();
  await selector.selectOption('cnx_claude');
  await expect(selector).toHaveValue('cnx_claude');
  await expect(page.getByRole('button', { name: 'sonnet', exact: true })).toBeVisible();
  await expect(input).toHaveValue('Keep my prompt');
  await expect(page.getByRole('button', { name: 'Remove image attachment 1', exact: true })).toBeVisible();
  await input.press('Enter');
  await expect.poll(() => starts.length).toBe(1);
  expect(starts[0]).toMatchObject({ platform: 'cnx_claude', prompt: 'Keep my prompt', send: { model: 'claudecode:anthropic/sonnet', agent: 'assistant' } });
  expect((starts[0] as { send: object }).send).not.toHaveProperty('reasoning');
  expect((starts[0] as { send: { images: unknown[] } }).send.images).toHaveLength(1);
  await expect(selector).toBeDisabled();
  await page.reload();
  await expect(selector).toHaveValue('cnx_claude');
  await expect(selector).toBeDisabled();
  await page.getByRole('alert').filter({ hasText: 'Temporary delivery failure' }).getByRole('button', { name: 'Retry', exact: true }).click();
  await expect.poll(() => retries.length).toBe(1);
  expect(retries[0]).toEqual(starts[0]);
  expect(starts).toHaveLength(1);
});
