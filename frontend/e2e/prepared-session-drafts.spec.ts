import { test, expect, installDefaultRoutes, MOCK_SESSION } from './fixtures';
import type { Page } from '@playwright/test';

/** Read a composer text record straight from the draft database. */
function storedText(page: Page, id: string) {
  return page.evaluate((key) => new Promise<{ text: string; revision: number } | undefined>((resolve, reject) => {
    const open = indexedDB.open('ocman.drafts.v1');
    open.onerror = () => reject(open.error);
    open.onsuccess = () => {
      const db = open.result;
      const request = db.transaction('texts').objectStore('texts').get(key);
      request.onsuccess = () => { db.close(); resolve(request.result); };
      request.onerror = () => { db.close(); reject(request.error); };
    };
  }), id);
}

async function prepareDraft(page: Page) {
  await page.route('**/api/sessions/prepare', (route) => route.fulfill({ json: {
    platform: 'opencode', agents: [{ name: 'build' }, { name: 'plan' }], commands: [],
    models: { hasProviders: true, models: [] }, liveConnection: false,
  } }));
  await page.route('**/api/worktree/list?*', (route) => route.fulfill({ json: { worktrees: [] } }));
  await page.route('**/api/git/info?*', (route) => route.fulfill({ json: {} }));
  const local = { remoteId: 'local', remoteName: 'This machine', platform: 'opencode', dir: '/repo' };
  const remote = { remoteId: 'box', remoteName: 'Build box', platform: 'r-box:opencode', dir: '/repo' };
  await page.route('**/api/sessions/resolve-targets', (route) => route.fulfill({ json: { candidates: [local, remote], remotes: [remote] } }));
}

test('a draft discarded in one tab is not resurrected by another tab\'s autosave', async ({ mockedPage: first }) => {
  const second = await first.context().newPage();
  await installDefaultRoutes(second);
  for (const page of [first, second]) {
    await prepareDraft(page);
    await page.goto('/session/new?dir=%2Frepo&draftId=shared-discard&title=Shared');
  }
  await second.getByRole('textbox').fill('typed in the second tab');
  await expect.poll(async () => (await storedText(first, 'shared-discard'))?.text).toBe('typed in the second tab');
  await first.getByLabel('Prepared sessions').getByRole('button', { name: 'Discard draft' }).click();
  // The second tab learns of the discard and moves off the retired identity.
  await expect(second).not.toHaveURL(/draftId=shared-discard/);
  await second.getByRole('textbox').fill('a new draft');
  const freshId = new URL(second.url()).searchParams.get('draftId')!;
  await expect.poll(async () => (await storedText(second, freshId))?.text).toBe('a new draft');
  await expect.poll(async () => (await storedText(first, 'shared-discard'))?.text).toBe('');
  await second.reload();
  // Only the fresh replacement draft remains; the retired identity stays retired.
  await expect(second.getByLabel('Prepared sessions').getByRole('button', { name: 'Discard draft' })).toHaveCount(1);
  await expect(second).not.toHaveURL(/draftId=shared-discard/);
  await expect(second.getByRole('textbox')).toHaveValue('a new draft');
});

test('Back skips a completed draft and reaches the preceding page', async ({ mockedPage: page }) => {
  await prepareDraft(page);
  await page.route('**/api/sessions/start', (route) => route.fulfill({ json: {
    sessionId: MOCK_SESSION.id, directory: '/repo', platform: 'opencode', remoteId: 'local', firstMessageSent: true,
  } }));
  await page.goto('/settings');
  await page.goto('/session/new?dir=%2Frepo&draftId=history-replacement');
  await page.getByRole('textbox').fill('Create this session');
  await page.getByRole('button', { name: 'Send message' }).click();
  await expect(page).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}$`));
  await page.goBack();
  await expect(page).toHaveURL(/\/settings$/);
});

for (const failMirror of [false, true]) test(`peer first-delivery ordering and owner recovery with localStorage unavailable: ${failMirror}`, async ({ mockedPage: first }) => {
  if (failMirror) await first.addInitScript(() => {
    const original = Storage.prototype.setItem;
    Storage.prototype.setItem = function (key, value) {
      if (key.startsWith('ocman.')) throw new Error('localStorage quota');
      original.call(this, key, value);
    };
  });
  const peer = await first.context().newPage();
  await installDefaultRoutes(peer);
  let finish!: () => void;
  const waiting = new Promise<void>((resolve) => { finish = resolve; });
  let uploads = 0;
  const messages: string[] = [];
  for (const page of [first, peer]) {
    await prepareDraft(page);
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(?:\\?|$)`), (route) => route.fulfill({ json: {
      session: { ...MOCK_SESSION, liveConnection: true }, messages: [], parts: [], totalMessages: 0,
    } }));
    await page.route('**/api/sessions/start', (route) => route.fulfill({ json: {
      sessionId: MOCK_SESSION.id, directory: '/repo', platform: 'opencode', remoteId: 'local', firstMessageSent: false,
    } }));
    await page.route('**/api/session/*/attachment?*', async (route) => {
      uploads++;
      await waiting;
      if (!page.isClosed()) await route.fulfill({ json: { path: '/tmp/first.txt', name: 'first.txt', mime: 'text/plain', size: 4 } });
    });
    await page.route('**/api/session/*/message*', async (route) => {
      messages.push(route.request().postDataJSON().message);
      await route.fulfill({ json: {} });
    });
    await page.goto('/session/new?dir=%2Frepo&draftId=first-delivery');
  }
  await first.locator('input[type="file"]').setInputFiles({ name: 'first.txt', mimeType: 'text/plain', buffer: Buffer.from('note') });
  await expect(first.getByText('first.txt')).toBeVisible();
  await first.getByRole('textbox').fill('First delivery');
  await first.getByRole('button', { name: 'Send message' }).click();
  await expect.poll(() => uploads).toBe(1);
  await expect(peer).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}$`));
  await expect(peer.getByRole('textbox')).toBeDisabled();
  expect(messages).toEqual([]);
  if (failMirror) {
    await first.close();
    finish();
    await expect(peer.getByRole('alert').filter({ hasText: 'outcome is unknown' })).toBeVisible({ timeout: 12000 });
    expect(messages).toEqual([]);
    peer.once('dialog', (dialog) => dialog.accept());
    await peer.getByRole('button', { name: 'Release first-delivery lock' }).click();
    await expect(peer.getByRole('textbox')).toBeEnabled();
    expect(uploads).toBe(1);
    expect(messages).toEqual([]);
    return;
  }
  finish();
  await expect(peer.getByRole('textbox')).toBeEnabled();
  await peer.getByRole('textbox').fill('Follow-up');
  await peer.getByRole('button', { name: 'Send message' }).click();
  await expect.poll(() => messages.length).toBe(2);
  expect(messages[0]).toContain('First delivery');
  expect(messages[1]).toBe('Follow-up');
});

test('two tabs starting the same draft create only one session and share completion', async ({ mockedPage: first }) => {
  const second = await first.context().newPage();
  await installDefaultRoutes(second);
  let starts = 0;
  let owner: typeof first | undefined;
  let finish!: () => void;
  const waiting = new Promise<void>((resolve) => { finish = resolve; });
  for (const page of [first, second]) {
    await prepareDraft(page);
    await page.route('**/api/sessions/start', async (route) => {
      starts++;
      owner = page;
      await waiting;
      await route.fulfill({ json: { sessionId: MOCK_SESSION.id, directory: '/repo', platform: 'opencode', remoteId: 'local', firstMessageSent: true } });
    });
    await page.goto('/session/new?dir=%2Frepo&draftId=shared&title=Shared');
    await page.getByRole('textbox').fill('Start only once.');
  }
  // Dispatch together: actionability waiting must not serialize away the race or wait on the shared lock.
  await Promise.all([first.getByRole('button', { name: 'Send message' }).dispatchEvent('click'), second.getByRole('button', { name: 'Send message' }).dispatchEvent('click')]);
  await expect.poll(() => starts).toBeGreaterThan(0);
  const waitingTab = owner === first ? second : first;
  await waitingTab.reload();
  await expect(waitingTab.getByRole('textbox')).toBeDisabled();
  await expect(waitingTab.getByRole('combobox', { name: 'Session machine' })).toBeDisabled();
  finish();
  await expect(first).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}$`));
  await expect(second).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}$`));
  expect(starts).toBe(1);
});

test('unavailable atomic storage fails visibly without starting a session', async ({ mockedPage: page }) => {
  await page.addInitScript(() => Object.defineProperty(window, 'indexedDB', { value: undefined }));
  let starts = 0;
  await page.route('**/api/sessions/start', (route) => { starts++; return route.fulfill({ json: {} }); });
  await prepareDraft(page);
  await page.goto('/session/new?dir=%2Frepo&draftId=unavailable');
  await page.getByRole('textbox').fill('Keep this prompt.');
  await page.getByRole('button', { name: 'Send message' }).click();
  await expect(page.getByTestId('conversation-composer').getByRole('alert')).toHaveText('This browser cannot coordinate session starts.');
  await expect(page.getByRole('textbox')).toHaveValue('Keep this prompt.');
  expect(starts).toBe(0);
});

test('an unusable atomic-store schema surfaces an error and keeps the prompt', async ({ mockedPage: page }) => {
  await prepareDraft(page);
  await page.route('**/fault-setup', (route) => route.fulfill({ contentType: 'text/html', body: '<!doctype html><title>Atomic-store setup</title>' }));
  await page.goto('/fault-setup');
  const stores = await page.evaluate(() => new Promise<number>((resolve, reject) => {
    const open = indexedDB.open('ocman.drafts.v1', 1);
    open.onsuccess = () => { const count = open.result.objectStoreNames.length; open.result.close(); resolve(count); };
    open.onerror = () => reject(open.error);
  }));
  expect(stores).toBe(0);
  let starts = 0;
  await page.route('**/api/sessions/start', (route) => { starts++; return route.fulfill({ json: {} }); });
  await page.goto('/session/new?dir=%2Frepo&draftId=broken');
  await page.getByRole('textbox').fill('Keep this prompt.');
  await page.getByRole('button', { name: 'Send message' }).click();
  await expect(page.getByTestId('conversation-composer').getByRole('alert')).toBeVisible();
  await expect(page.getByRole('textbox')).toHaveValue('Keep this prompt.');
  expect(starts).toBe(0);
});

test('a failed start preserves its prompt and releases the atomic claim for an explicit retry', async ({ mockedPage: page }) => {
  await prepareDraft(page);
  let starts = 0;
  await page.route('**/api/sessions/start', (route) => {
    starts++;
    return starts === 1 ? route.fulfill({ status: 500, json: { error: 'First start failed' } })
      : route.fulfill({ json: { sessionId: MOCK_SESSION.id, directory: '/repo', platform: 'opencode', remoteId: 'local', firstMessageSent: true } });
  });
  await page.goto('/session/new?dir=%2Frepo&draftId=retry');
  await page.getByRole('textbox').fill('Keep this prompt for retry.');
  await page.getByRole('button', { name: 'Send message' }).click();
  await expect(page.getByRole('alert')).toContainText('First start failed');
  await expect(page.getByRole('textbox')).toHaveValue('Keep this prompt for retry.');
  await expect(page.getByRole('textbox')).not.toBeDisabled();
  await page.getByRole('button', { name: 'Send message' }).click();
  await expect(page).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}$`));
  expect(starts).toBe(2);
});

test('a failed start keeps its prompt and failure across reload without replaying', async ({ mockedPage: page }) => {
  await prepareDraft(page);
  let starts = 0;
  await page.route('**/api/sessions/start', (route) => { starts++; return route.fulfill({ status: 500, json: { error: 'Retryable start failure' } }); });
  await page.goto('/session/new?dir=%2Frepo&draftId=quota');
  await page.getByRole('textbox').fill('Restore this prompt.');
  await page.getByRole('button', { name: 'Send message' }).click();
  await expect(page.getByRole('alert')).toContainText('Retryable start failure');
  await page.reload();
  await expect(page.getByRole('textbox')).not.toBeDisabled();
  await expect(page.getByRole('textbox')).toHaveValue('Restore this prompt.');
  await expect(page.getByRole('alert')).toContainText('Retryable start failure');
  expect(starts).toBe(1);
});

test('an aborted terminal transaction remains visible and can be safely repaired before retry', async ({ mockedPage: page }) => {
  await prepareDraft(page);
  await page.addInitScript(() => {
    const controlled = window as Window & { abortTerminal: boolean };
    controlled.abortTerminal = true;
    const put = IDBObjectStore.prototype.put;
    IDBObjectStore.prototype.put = function (value, key) {
      if (controlled.abortTerminal && (value.sessionId || value.error)) {
        this.transaction.abort();
        return {} as IDBRequest<IDBValidKey>;
      }
      return put.call(this, value, key);
    };
  });
  let starts = 0;
  await page.route('**/api/sessions/start', (route) => {
    starts++;
    return starts === 1 ? route.fulfill({ status: 500, json: { error: 'First creation failed' } })
      : route.fulfill({ json: { sessionId: MOCK_SESSION.id, directory: '/repo', platform: 'opencode', remoteId: 'local', firstMessageSent: true } });
  });
  await page.goto('/session/new?dir=%2Frepo&draftId=terminal');
  await page.getByRole('textbox').fill('Resilient prompt');
  await page.getByRole('button', { name: 'Send message' }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'Could not save' })).toBeVisible();
  await expect(page.getByRole('textbox')).toHaveValue('Resilient prompt');
  expect(starts).toBe(1);
  // Storage recovers: the known failure is written first, then the retry may claim the draft.
  await page.evaluate(() => { (window as Window & { abortTerminal: boolean }).abortTerminal = false; });
  await page.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'Could not save' })).toHaveCount(0);
  await page.getByRole('button', { name: 'Send message' }).click();
  await expect(page).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}$`));
  expect(starts).toBe(2);
});

test('a rejected competing prompt cannot leave the winning failed claim pending', async ({ mockedPage: first }) => {
  const second = await first.context().newPage();
  await installDefaultRoutes(second);
  let owner: typeof first | undefined;
  let finish!: () => void;
  const waiting = new Promise<void>((resolve) => { finish = resolve; });
  let starts = 0;
  for (const [page, text] of [[first, 'First prompt'], [second, 'Other prompt']] as const) {
    await prepareDraft(page);
    await page.route('**/api/sessions/start', async (route) => {
      starts++;
      owner = page;
      await waiting;
      await route.fulfill({ status: 500, json: { error: 'First creation failed' } });
    });
    await page.goto('/session/new?dir=%2Frepo&draftId=competing');
    await page.getByRole('textbox').fill(text);
  }
  await Promise.all([first.getByRole('button', { name: 'Send message' }).dispatchEvent('click'), second.getByRole('button', { name: 'Send message' }).dispatchEvent('click')]);
  await expect.poll(() => starts).toBe(1);
  const losingText = owner === first ? 'Other prompt' : 'First prompt';
  await expect.poll(async () => (await storedText(first, 'competing'))?.text).toBe(losingText);
  finish();
  await expect(owner!.getByRole('alert')).toContainText('First creation failed');
  await Promise.all([first.reload(), second.reload()]);
  await expect(first.getByRole('textbox')).not.toBeDisabled();
  await expect(second.getByRole('textbox')).not.toBeDisabled();
  expect(starts).toBe(1);
});

test('cleared and discarded failed prompts stay empty after reload and an old-URL revisit', async ({ mockedPage: page }) => {
  await prepareDraft(page);
  await page.route('**/api/sessions/start', (route) => route.fulfill({ status: 500, json: { error: 'Creation failed' } }));
  const url = '/session/new?dir=%2Frepo&draftId=clear';
  await page.goto(url);
  await page.getByRole('textbox').fill('Do not resurrect this prompt.');
  await page.getByRole('button', { name: 'Send message' }).click();
  await expect(page.getByTestId('conversation-composer').getByRole('alert')).toContainText('Creation failed');
  await expect(page.getByRole('textbox')).not.toBeDisabled();
  await page.getByRole('textbox').fill('');
  await expect.poll(async () => (await storedText(page, 'clear'))?.revision).toBeGreaterThan(0);
  await page.reload();
  await expect(page.getByRole('textbox')).toHaveValue('');
  await page.getByLabel('Prepared sessions').getByRole('button', { name: 'Discard draft' }).click();
  await page.goto(url);
  await expect(page.getByRole('textbox')).toHaveValue('');
});

test('prepares multiple sidebar drafts without starting sessions', async ({ mockedPage: page }, testInfo) => {
  let starts = 0;
  await page.route('**/api/sessions/start', async (route) => {
    starts++;
    await route.fulfill({ json: {} });
  });
  await prepareDraft(page);
  await page.goto('/session/new?dir=%2Frepo&draftId=first&title=Plan+the+API');
  await page.getByRole('textbox').fill('Design the API before implementation.');
  await expect.poll(async () => (await storedText(page, 'first'))?.text).toContain('Design the API');
  await page.goto('/session/new?dir=%2Frepo&draftId=second&title=Prepare+the+UI');
  await expect(page.getByRole('textbox')).toHaveValue('');
  await page.getByRole('textbox').fill('Prepare the sidebar UI.');
  const drafts = page.getByLabel('Prepared sessions');
  await drafts.getByRole('button', { name: /Plan the API/ }).click();
  await expect(page.getByRole('textbox')).toHaveValue('Design the API before implementation.');
  await drafts.getByRole('button', { name: /Prepare the UI/ }).click();
  await expect(page.getByRole('textbox')).toHaveValue('Prepare the sidebar UI.');
  await expect.poll(async () => (await storedText(page, 'second'))?.text).toContain('Prepare the sidebar UI.');
  await page.reload();
  await expect(drafts.getByRole('button', { name: /Plan the API/ })).toBeVisible();
  await expect(page.getByRole('textbox')).toHaveValue('Prepare the sidebar UI.');
  const screenshot = testInfo.outputPath('prepared-session-drafts.png');
  await page.screenshot({ path: screenshot });
  await testInfo.attach('prepared-session-drafts', { path: screenshot, contentType: 'image/png' });
  await page.setViewportSize({ width: 390, height: 844 });
  const toggle = page.getByTestId('mobile-sessions-toggle');
  await toggle.click();
  await drafts.getByRole('button', { name: /Prepare the UI/ }).click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await toggle.click();
  await drafts.getByRole('button', { name: /Plan the API/ }).click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await expect(page.getByRole('textbox')).toHaveValue('Design the API before implementation.');
  await toggle.click();
  await drafts.getByRole('button', { name: /Prepare the UI/ }).click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await toggle.click();
  await drafts.getByRole('button', { name: 'Discard draft' }).first().click();
  await expect(drafts.getByRole('button', { name: /Plan the API/ })).toHaveCount(0);
  await toggle.click();
  await expect(page.getByRole('textbox')).toHaveValue('Prepare the sidebar UI.');
  expect(starts).toBe(0);
});
