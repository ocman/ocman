import { test, expect, MOCK_SESSION } from './fixtures';
import type { BackendConversationDraft } from '../src/lib/backendConversationDrafts';

for (const width of [1280, 390]) for (const choice of ['Keep my edits', 'Use server version']) test(`resolves a draft conflict with ${choice} at ${width}px`, async ({ mockedPage: page }) => {
  await page.setViewportSize({ width, height: 844 });
  const id = 'ocm_01234567-89ab-4cde-8fab-0123456789ab';
  let draft: BackendConversationDraft = { id, metadata: { draftId: id, directory: MOCK_SESSION.directory, remoteId: 'local', platform: 'opencode', text: 'Original' },
    revision: 'A', phase: 'draft', createdAt: 1, steps: {} };
  let submitted = '';
  await page.route('**/api/sessions/prepare', (route) => route.fulfill({ json: {
    platform: 'opencode', agents: [], commands: [], models: { hasProviders: true, models: [] }, liveConnection: true,
  } }));
  await page.route('**/api/git/info*', (route) => route.fulfill({ json: {} }));
  await page.route('**/api/worktree/list*', (route) => route.fulfill({ json: { worktrees: [] } }));
  await page.route('**/api/session-drafts**', (route) => {
    const request = route.request();
    if (request.method() === 'PUT') {
      if (request.headers()['if-match'] !== draft.revision) return route.fulfill({ status: 409, body: 'Draft changed in another browser' });
      draft = { ...draft, metadata: request.postDataJSON() };
      return route.fulfill({ json: draft });
    }
    if (request.url().endsWith('/start')) {
      submitted = request.postDataJSON().send.message;
      draft = { ...draft, phase: 'starting', request: request.postDataJSON(), submittedAt: Date.now() };
      return route.fulfill({ status: 202, json: draft });
    }
    return route.fulfill({ json: new URL(request.url()).pathname === '/api/session-drafts' ? [draft] : draft });
  });
  await page.goto(`/session/${id}?dir=${encodeURIComponent(MOCK_SESSION.directory)}&platform=opencode&draftId=${id}`);
  const composer = page.getByTestId('conversation-composer').getByRole('textbox');
  await expect(composer).toHaveValue('Original');
  // The other browser commits B after this browser has recovered A.
  draft = { ...draft, revision: 'B', metadata: { ...draft.metadata, text: 'Peer edits', title: 'Peer title' } };
  await composer.fill('My edits');
  const alert = page.getByRole('alert').filter({ hasText: 'Draft not saved' });
  await alert.getByRole('button', { name: /Retry/ }).click();
  const dialog = page.getByRole('dialog', { name: 'Resolve draft conflict' });
  await expect(dialog.getByRole('textbox', { name: 'My version' })).toHaveValue('My edits');
  await expect(dialog.getByRole('textbox', { name: 'Server version' })).toHaveValue('Peer edits');
  await dialog.getByRole('button', { name: choice, exact: true }).click();
  const expected = choice === 'Keep my edits' ? 'My edits' : 'Peer edits';
  await expect(dialog).not.toBeVisible();
  await expect(composer).toHaveValue(expected);
  await composer.press('Enter');
  await expect.poll(() => submitted).toBe(expected);
});
