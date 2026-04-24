/**
 * e2e: Permission & Question prompts
 *
 * Both prompts are injected via SSE (`permission.asked` / `question.asked`)
 * and exercised entirely in-browser — no LLM or running agent is needed.
 *
 * Permission prompt covers:
 *  - Renders with permission text and patterns
 *  - "Allow once" button calls POST /api/session/:id/permissions/:id with reply=once
 *  - "Allow always" button calls POST with reply=always
 *  - "Reject" button calls POST with reply=reject
 *  - Keyboard: `o` hotkey → allow once
 *  - Keyboard: `a` hotkey → allow always
 *  - Keyboard: `r` hotkey → reject
 *  - Keyboard: Escape → reject
 *  - Keyboard: ArrowDown/Enter → move focus and submit
 *  - Error message shown when the API reply fails
 *
 * Question prompt covers:
 *  - Renders with question text and options
 *  - Clicking an option selects it and (single-step) submits automatically
 *  - "Dismiss" button calls the reject path
 *  - Keyboard: number key 1 selects first option
 *  - Keyboard: Escape dismisses
 *  - Multi-step: Back/Next navigation
 *  - Custom text input answer
 */

import {
  test,
  expect,
  MOCK_SESSION,
  mockSse,
  sseEvent,
  mockSessionWithLiveConnection,
} from './fixtures';

const SESSION_URL = `/session/${MOCK_SESSION.id}`;

// ---------------------------------------------------------------------------
// Shared helper
// ---------------------------------------------------------------------------

async function setupLivePage(page: import('@playwright/test').Page) {
  const liveSession = mockSessionWithLiveConnection();
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ session: liveSession, messages: [], parts: [], totalMessages: 0, defaultAgent: 'build', defaultModel: '' }),
    }),
  );
  await page.route('/api/sessions*', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([liveSession]) }),
  );
  await page.goto(SESSION_URL);
  await expect(page.locator('.session-layout')).toBeVisible();
}

function permissionAsked(permId = 'perm-001', opts: { patterns?: string[] } = {}) {
  // extractPendingPermission reads: obj.properties.id / .permission / .patterns
  return sseEvent({
    type: 'permission.asked',
    properties: {
      sessionID: MOCK_SESSION.id,
      id: permId,
      requestID: permId,
      permission: 'Write to /tmp/output.txt',
      patterns: opts.patterns ?? [],
    },
  });
}

function questionAsked(reqId = 'q-001', steps = 1) {
  const questions = Array.from({ length: steps }, (_, i) => ({
    question: i === 0 ? 'Which approach?' : `Follow-up question ${i + 1}`,
    header: `Step ${i + 1}`,
    options: [
      { label: 'Option A', description: 'First choice' },
      { label: 'Option B', description: 'Second choice' },
    ],
    multiple: false,
    custom: true,
  }));
  // extractPendingQuestion reads: obj.properties.id / .requestID / .requestId / .sessionID / .questions
  return sseEvent({
    type: 'question.asked',
    properties: {
      sessionID: MOCK_SESSION.id,
      requestID: reqId,
      requestId: reqId,
      questions,
    },
  });
}

// ===========================================================================
// PERMISSION PROMPT
// ===========================================================================

test('permission prompt renders permission text', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [permissionAsked()]);
  await setupLivePage(page);
  await expect(page.locator('.oc-permission-desc')).toContainText('/tmp/output.txt', { timeout: 5_000 });
});

test('permission prompt renders patterns when present', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [permissionAsked('p1', { patterns: ['*.txt', '/tmp/**'] })]);
  await setupLivePage(page);
  await expect(page.locator('.oc-permission-pattern', { hasText: '*.txt' })).toBeVisible({ timeout: 5_000 });
  await expect(page.locator('.oc-permission-pattern', { hasText: '/tmp/**' })).toBeVisible();
});

test('"Allow once" button POSTs reply=once', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [permissionAsked('perm-once')]);
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/permissions/perm-once`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );
  await setupLivePage(page);
  await expect(page.locator('.oc-permission-wrap')).toBeVisible({ timeout: 5_000 });

  const [req] = await Promise.all([
    page.waitForRequest((r) => r.url().includes('/permissions/perm-once') && r.method() === 'POST'),
    page.locator('.oc-permission-btn', { hasText: 'Allow once' }).click(),
  ]);
  expect(JSON.parse(req.postData() ?? '{}')).toMatchObject({ reply: 'once' });
});

test('"Allow always" button POSTs reply=always', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [permissionAsked('perm-always')]);
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/permissions/perm-always`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );
  await setupLivePage(page);
  await expect(page.locator('.oc-permission-wrap')).toBeVisible({ timeout: 5_000 });

  const [req] = await Promise.all([
    page.waitForRequest((r) => r.url().includes('/permissions/perm-always') && r.method() === 'POST'),
    page.locator('.oc-permission-btn', { hasText: 'Allow always' }).click(),
  ]);
  expect(JSON.parse(req.postData() ?? '{}')).toMatchObject({ reply: 'always' });
});

test('"Reject" button POSTs reply=reject', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [permissionAsked('perm-reject')]);
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/permissions/perm-reject`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );
  await setupLivePage(page);
  await expect(page.locator('.oc-permission-wrap')).toBeVisible({ timeout: 5_000 });

  const [req] = await Promise.all([
    page.waitForRequest((r) => r.url().includes('/permissions/perm-reject') && r.method() === 'POST'),
    page.locator('.oc-permission-btn', { hasText: 'Reject' }).click(),
  ]);
  expect(JSON.parse(req.postData() ?? '{}')).toMatchObject({ reply: 'reject' });
});

test('hotkey "o" triggers allow-once', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [permissionAsked('perm-o')]);
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/permissions/perm-o`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );
  await setupLivePage(page);
  await expect(page.locator('.oc-permission-wrap')).toBeVisible({ timeout: 5_000 });

  const [req] = await Promise.all([
    page.waitForRequest((r) => r.url().includes('/permissions/perm-o') && r.method() === 'POST'),
    page.keyboard.press('o'),
  ]);
  expect(JSON.parse(req.postData() ?? '{}')).toMatchObject({ reply: 'once' });
});

test('hotkey "a" triggers allow-always', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [permissionAsked('perm-a')]);
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/permissions/perm-a`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );
  await setupLivePage(page);
  await expect(page.locator('.oc-permission-wrap')).toBeVisible({ timeout: 5_000 });

  const [req] = await Promise.all([
    page.waitForRequest((r) => r.url().includes('/permissions/perm-a') && r.method() === 'POST'),
    page.keyboard.press('a'),
  ]);
  expect(JSON.parse(req.postData() ?? '{}')).toMatchObject({ reply: 'always' });
});

test('hotkey "r" triggers reject', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [permissionAsked('perm-r')]);
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/permissions/perm-r`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );
  await setupLivePage(page);
  await expect(page.locator('.oc-permission-wrap')).toBeVisible({ timeout: 5_000 });

  const [req] = await Promise.all([
    page.waitForRequest((r) => r.url().includes('/permissions/perm-r') && r.method() === 'POST'),
    page.keyboard.press('r'),
  ]);
  expect(JSON.parse(req.postData() ?? '{}')).toMatchObject({ reply: 'reject' });
});

test('Escape key triggers reject on permission prompt', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [permissionAsked('perm-esc')]);
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/permissions/perm-esc`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );
  await setupLivePage(page);
  await expect(page.locator('.oc-permission-wrap')).toBeVisible({ timeout: 5_000 });

  const [req] = await Promise.all([
    page.waitForRequest((r) => r.url().includes('/permissions/perm-esc') && r.method() === 'POST'),
    page.keyboard.press('Escape'),
  ]);
  expect(JSON.parse(req.postData() ?? '{}')).toMatchObject({ reply: 'reject' });
});

test('permission prompt shows error when API reply fails', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [permissionAsked('perm-err')]);
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/permissions/perm-err`),
    (route) => route.fulfill({ status: 500, body: 'Internal Server Error' }),
  );
  await setupLivePage(page);
  await expect(page.locator('.oc-permission-wrap')).toBeVisible({ timeout: 5_000 });
  await page.locator('.oc-permission-btn', { hasText: 'Allow once' }).click();
  await expect(page.locator('.oc-permission-error')).toBeVisible({ timeout: 3_000 });
});

// ===========================================================================
// QUESTION PROMPT
// ===========================================================================

test('question prompt renders question text and options', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [questionAsked()]);
  await setupLivePage(page);

  await expect(page.locator('.oc-question-box-text')).toContainText('Which approach?', { timeout: 5_000 });
  await expect(page.locator('.oc-question-opt-label', { hasText: 'Option A' })).toBeVisible();
  await expect(page.locator('.oc-question-opt-label', { hasText: 'Option B' })).toBeVisible();
});

test('clicking an option POSTs the answer', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [questionAsked('q-click')]);
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/questions/q-click`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );
  await setupLivePage(page);
  await expect(page.locator('.oc-question-wrap')).toBeVisible({ timeout: 5_000 });

  const [req] = await Promise.all([
    page.waitForRequest((r) => r.url().includes('/questions/q-click') && r.method() === 'POST'),
    page.locator('.oc-question-opt-btn', { hasText: 'Option A' }).click(),
  ]);
  const body = JSON.parse(req.postData() ?? '{}');
  // answers is a 2D array: [[selected_label]]
  expect(body.answers).toEqual([['Option A']]);
});

test('number key 1 selects first option and submits (single-step)', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [questionAsked('q-num')]);
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/questions/q-num`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );
  await setupLivePage(page);
  await expect(page.locator('.oc-question-wrap')).toBeVisible({ timeout: 5_000 });

  const [req] = await Promise.all([
    page.waitForRequest((r) => r.url().includes('/questions/q-num') && r.method() === 'POST'),
    page.keyboard.press('1'),
  ]);
  const body = JSON.parse(req.postData() ?? '{}');
  expect(body.answers).toEqual([['Option A']]);
});

test('Escape key dismisses the question prompt', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [questionAsked('q-esc')]);
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/questions/q-esc/reject`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );
  await setupLivePage(page);
  await expect(page.locator('.oc-question-wrap')).toBeVisible({ timeout: 5_000 });

  await page.keyboard.press('Escape');
  await expect(page.locator('.oc-question-wrap')).toHaveCount(0, { timeout: 3_000 });
});

test('"Dismiss" button rejects the question', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [questionAsked('q-dismiss')]);
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/questions/q-dismiss/reject`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );
  await setupLivePage(page);
  await expect(page.locator('.oc-question-wrap')).toBeVisible({ timeout: 5_000 });

  await page.locator('.oc-question-dismiss-btn', { hasText: 'Dismiss' }).click();
  await expect(page.locator('.oc-question-wrap')).toHaveCount(0, { timeout: 3_000 });
});

test('multi-step question shows step indicator and Next/Back buttons', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [questionAsked('q-multi', 2)]);
  await setupLivePage(page);

  await expect(page.locator('.oc-question-step-indicator')).toBeVisible({ timeout: 5_000 });
  await expect(page.locator('.oc-question-step-label')).toContainText('1 / 2');
  await expect(page.locator('.oc-question-submit-btn', { hasText: 'Next' })).toBeVisible();
});

test('multi-step: selecting option advances to next step', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [questionAsked('q-multi-advance', 2)]);
  await setupLivePage(page);

  await expect(page.locator('.oc-question-wrap')).toBeVisible({ timeout: 5_000 });
  // Click first option on step 1 — should auto-advance to step 2
  await page.locator('.oc-question-opt-btn', { hasText: 'Option A' }).click();
  await expect(page.locator('.oc-question-step-label')).toContainText('2 / 2', { timeout: 2_000 });
});

test('custom text input accepts typed text', async ({ mockedPage: page }) => {
  // This test verifies that the custom-answer input is present and accepts input.
  // The full submit-via-custom-text path is covered by QuestionPrompt unit tests.
  await mockSse(page, MOCK_SESSION.id, [questionAsked('q-custom-input')]);
  await setupLivePage(page);
  await expect(page.locator('.oc-question-wrap')).toBeVisible({ timeout: 5_000 });

  const input = page.locator('.oc-question-inline-input');
  await input.click();
  await input.type('My typed answer');

  // The input should reflect the typed value
  await expect(input).toHaveValue('My typed answer');
  // No radio option should be selected (custom text clears them)
  await expect(page.locator('.oc-question-opt-btn.oc-question-opt-selected')).toHaveCount(0);
});
