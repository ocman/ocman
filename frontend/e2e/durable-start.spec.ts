import { test, expect, MOCK_SESSION } from './fixtures';
import type { BackendConversationDraft } from '../src/lib/backendConversationDrafts';

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
