import { test, expect, MOCK_SESSION } from './fixtures';
import type { BackendConversationDraft } from '../src/lib/backendConversationDrafts';

test('discards a failed remote draft from the production sidebar and keeps it discarded after reload', async ({ mockedPage: page }) => {
  const id = 'ocm_01234567-89ab-4cde-8fab-0123456789ab';
  let draft: BackendConversationDraft = { id, metadata: { draftId: id, directory: MOCK_SESSION.directory, remoteId: 'box', title: 'Stuck remote draft' },
    phase: 'failed', createdAt: Date.now(), steps: {}, error: 'The remote needs an update.' };
  let deletes = 0;
  await page.route('**/api/session-drafts**', (route) => {
    const request = route.request();
    if (request.method() === 'DELETE') {
      deletes++;
      draft = { ...draft, phase: 'discarded', metadata: { draftId: id, directory: '' } };
      return route.fulfill({ status: 204 });
    }
    return route.fulfill({ json: new URL(request.url()).pathname === '/api/session-drafts' ? [draft] : draft });
  });
  await page.goto(`/session/${id}?dir=${encodeURIComponent(MOCK_SESSION.directory)}&remoteId=box&draftId=${id}`);
  const sidebar = page.getByTestId('session-sidebar');
  const row = sidebar.getByRole('button').filter({ hasText: 'Stuck remote draft' });
  await expect(row).toHaveCount(1);
  await row.hover();
  await row.getByRole('button', { name: 'Archive session', exact: true }).click();
  await expect.poll(() => deletes).toBe(1);
  await expect(sidebar.getByText('Stuck remote draft')).toHaveCount(0);
  await expect.poll(() => new URL(page.url()).pathname).not.toBe(`/session/${id}`);
  await page.reload();
  await expect(sidebar.getByText('Stuck remote draft')).toHaveCount(0);
  expect(deletes).toBe(1);
});

test('recovers server-only text and selections before allowing draft autosave', async ({ mockedPage: page }) => {
  const id = 'ocm_01234567-89ab-4cde-8fab-0123456789ab';
  const metadata = { draftId: id, directory: MOCK_SESSION.directory, platform: 'opencode', text: 'Server-only prompt', title: 'Saved title', agent: 'plan' };
  let release!: () => void;
  const recovery = new Promise<void>((resolve) => { release = resolve; });
  const writes: unknown[] = [];
  await page.route('**/api/session-drafts**', async (route) => {
    if (route.request().method() === 'PUT') {
      writes.push(route.request().postDataJSON());
      return route.fulfill({ status: 204 });
    }
    await recovery;
    return route.fulfill({ json: [{ id, metadata, phase: 'draft', createdAt: 1, steps: {} }] });
  });
  await page.goto(`/session/${id}?dir=${encodeURIComponent(MOCK_SESSION.directory)}&platform=opencode&draftId=${id}`);
  await expect(page.getByRole('textbox')).toHaveCount(0);
  expect(writes).toEqual([]);
  release();
  await expect(page.getByRole('textbox')).toHaveValue('Server-only prompt');
  await expect.poll(() => writes.length).toBeGreaterThan(0);
  expect(writes).toEqual(expect.arrayContaining([expect.objectContaining({ text: 'Server-only prompt', title: 'Saved title', agent: 'plan' })]));
});

test('persists first input, keeps the sidebar ID and queues follow-ups during setup', async ({ mockedPage: page }) => {
  const id = 'ocm_01234567-89ab-4cde-8fab-0123456789ab';
  let draft: BackendConversationDraft = { id, metadata: { draftId: id, directory: MOCK_SESSION.directory, platform: 'opencode' },
    phase: 'draft', createdAt: Date.now(), steps: {} };
  const queued: { id: string; text: string; hasImages: boolean }[] = [];
  let acknowledge!: () => void;
  const persisted = new Promise<void>((resolve) => { acknowledge = resolve; });
  await page.route('**/api/sessions/prepare', (route) => route.fulfill({ json: {
    platform: 'opencode', agents: [], commands: [], models: { hasProviders: true, models: [] }, liveConnection: true,
  } }));
  await page.route('**/api/git/info*', (route) => route.fulfill({ json: {} }));
  await page.route('**/api/worktree/list*', (route) => route.fulfill({ json: { worktrees: [] } }));
  await page.route(`**/api/session/${id}/queue*`, (route) => route.fulfill({ json: queued }));
  await page.route('**/api/session-drafts**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path.endsWith('/start')) {
      draft = { ...draft, request: request.postDataJSON(), submittedAt: Date.now(), phase: 'starting', steps: { platform: 'active' } };
      await persisted;
      return route.fulfill({ status: 202, json: draft });
    }
    if (path.endsWith('/messages')) {
      queued.push({ id: `q${queued.length}`, text: request.postDataJSON().message, hasImages: false });
      return route.fulfill({ status: 204 });
    }
    if (request.method() === 'PUT') {
      draft = { ...draft, metadata: request.postDataJSON() };
      return route.fulfill({ status: 204 });
    }
    return route.fulfill({ json: path === '/api/session-drafts' ? [draft] : draft });
  });
  await page.goto(`/session/${id}?dir=${encodeURIComponent(MOCK_SESSION.directory)}&platform=opencode&draftId=${id}`);
  const input = page.getByRole('textbox');
  await input.fill('First prompt');
  await input.press('Enter');
  await expect(input).toBeDisabled();
  await expect(input).toHaveValue('First prompt');
  acknowledge();
  await expect(input).toBeEnabled();
  await expect(input).toHaveValue('');
  const timestamp = page.getByTestId('launch-timestamp');
  const prompt = page.getByTestId('pending-prompt');
  expect((await timestamp.boundingBox())!.y).toBeLessThan((await prompt.boundingBox())!.y);
  await expect(page.getByTestId('start-step-platform')).toContainText('Launching');
  await input.fill('Follow-up while setup runs');
  await input.press('Enter');
  await expect(input).toHaveValue('');
  await expect.poll(() => queued.map((message) => message.text)).toEqual(['Follow-up while setup runs']);
  expect(new URL(page.url()).pathname).toBe(`/session/${id}`);
  await page.reload();
  await expect(page.getByTestId('pending-prompt')).toHaveText('First prompt');
  await expect(page.getByRole('textbox')).toBeEnabled();
});
