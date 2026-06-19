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
 *  - Keyboard: `a` hotkey → allow once
 *  - Keyboard: `A` hotkey → allow always
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
  MOCK_SESSION_2,
  mockSse,
  sseEvent,
  mockSessionWithLiveConnection,
} from './fixtures';

const SESSION_URL = `/session/${MOCK_SESSION.id}`;

// PermissionPrompt suppresses affirmative actions (Allow once / Allow always /
// confirm) for a short settle window after the prompt mounts, to absorb an
// in-flight keystroke that would otherwise accidentally accept a permission.
// Keep this in sync with SETTLE_MS in PermissionPrompt.tsx. e2e tests that
// drive affirmative hotkeys must wait past this window before pressing.
const PERMISSION_SETTLE_MS = 350;

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

  // Two-step: "Allow always" opens a confirmation screen, then "Confirm"
  // actually submits. See PermissionPrompt.tsx.
  await page.locator('.oc-permission-btn', { hasText: 'Allow always' }).click();
  await expect(page.locator('.oc-permission-wrap[aria-label="Confirm always allow"]')).toBeVisible();
  const [req] = await Promise.all([
    page.waitForRequest((r) => r.url().includes('/permissions/perm-always') && r.method() === 'POST'),
    page.locator('.oc-permission-btn', { hasText: 'Confirm' }).click(),
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

test('hotkey "a" triggers allow-once', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [permissionAsked('perm-a')]);
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/permissions/perm-a`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );
  await setupLivePage(page);
  await expect(page.locator('.oc-permission-wrap')).toBeVisible({ timeout: 5_000 });

  // Wait out the mount-time settle window so the affirmative hotkey isn't
  // swallowed as an in-flight keystroke.
  await page.waitForTimeout(PERMISSION_SETTLE_MS + 50);

  const [req] = await Promise.all([
    page.waitForRequest((r) => r.url().includes('/permissions/perm-a') && r.method() === 'POST'),
    page.keyboard.press('a'),
  ]);
  expect(JSON.parse(req.postData() ?? '{}')).toMatchObject({ reply: 'once' });
});

test('hotkey "Shift+A" triggers allow-always', async ({ mockedPage: page }) => {
  await mockSse(page, MOCK_SESSION.id, [permissionAsked('perm-A')]);
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/permissions/perm-A`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );
  await setupLivePage(page);
  await expect(page.locator('.oc-permission-wrap')).toBeVisible({ timeout: 5_000 });

  // Wait out the mount-time settle window so the affirmative hotkey isn't
  // swallowed as an in-flight keystroke.
  await page.waitForTimeout(PERMISSION_SETTLE_MS + 50);

  // Two-step: Shift+A opens the confirmation screen with Cancel focused by
  // default (CONFIRM_DEFAULT_IDX = 1), so Tab switches focus to Confirm,
  // and Enter submits.
  await page.keyboard.press('Shift+A');
  await expect(page.locator('.oc-permission-wrap[aria-label="Confirm always allow"]')).toBeVisible();
  await page.keyboard.press('Tab');
  const [req] = await Promise.all([
    page.waitForRequest((r) => r.url().includes('/permissions/perm-A') && r.method() === 'POST'),
    page.keyboard.press('Enter'),
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

// ===========================================================================
// INSTANT PROMPT ON SESSION SWITCH (regression: prompt was delayed until sidebar poll)
// ===========================================================================

/**
 * When navigating directly to a session that already has a pending permission
 * (or question), the prompt must appear immediately — without waiting for the
 * sidebar poll cycle to fire the reverse-sync effect.
 *
 * The bug: SessionDetail's reverse-sync effect is gated on `sidebarHasPerm`
 * (derived from the sidebar poll), so the permission prompt doesn't show
 * until the first /api/sessions poll completes (~100–500 ms) AND
 * /api/session/{id}/permissions resolves on top of that.
 *
 * The fix: also trigger the `listPermissions`/`listQuestions` fetch when the
 * initial REST response for the session has `pendingPermission: true` /
 * `pendingQuestion: true`, without waiting for the sidebar poll.
 *
 * This test sets up:
 *  - /api/session/{id} → pendingPermission: true (no SSE event)
 *  - /api/sessions     → no pendingPermission flag (sidebar poll must NOT be
 *                        the trigger; we omit it to isolate the fast path)
 *  - /api/session/{id}/permissions → the actual permission data
 *
 * And asserts the PermissionPrompt is visible within 1 s of landing on the
 * page — well below the sidebar-poll round-trip time.
 */
test('permission prompt appears immediately on session load when pendingPermission is true', async ({ mockedPage: page }) => {
  const PERM_ID = 'perm-instant';
  const liveSession = {
    ...mockSessionWithLiveConnection(),
    pendingPermission: true,
  };

  // REST detail: session has pendingPermission: true.
  // No SSE permission.asked event is delivered — the prompt must come
  // purely from the initial fetch, not from the stream.
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        session: liveSession,
        messages: [],
        parts: [],
        totalMessages: 0,
        defaultAgent: 'build',
        defaultModel: '',
      }),
    }),
  );

  // Permissions endpoint: return the full detail for the waiting prompt.
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/permissions`),
    (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          {
            id: PERM_ID,
            requestID: PERM_ID,
            sessionID: MOCK_SESSION.id,
            permission: 'Write to /tmp/instant.txt',
            patterns: [],
          },
        ]),
      }),
  );

  // Sidebar: session WITHOUT pendingPermission flag — ensures the prompt
  // is not triggered by the sidebar poll path but by the initial load.
  await page.route('/api/sessions*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([{ ...liveSession, pendingPermission: false }]),
    }),
  );

  // SSE: abort immediately — no permission.asked event will arrive.
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}/events`), (route) =>
    route.abort(),
  );

  await page.goto(`/session/${MOCK_SESSION.id}`);
  await expect(page.locator('.session-layout')).toBeVisible();

  // The prompt must appear within 1 s — fast enough to rule out the
  // sidebar-poll path (which requires at least one /api/sessions round-trip
  // plus a /api/session/{id}/permissions fetch on top).
  await expect(page.locator('.oc-permission-desc')).toContainText('/tmp/instant.txt', { timeout: 1_000 });
});

/**
 * Same for question prompts: when the initial REST response has
 * `pendingQuestion: true`, the QuestionPrompt must appear immediately
 * without waiting for the sidebar poll.
 */
test('question prompt appears immediately on session load when pendingQuestion is true', async ({ mockedPage: page }) => {
  const Q_ID = 'q-instant';
  const liveSession = {
    ...mockSessionWithLiveConnection(),
    pendingQuestion: true,
  };

  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        session: liveSession,
        messages: [],
        parts: [],
        totalMessages: 0,
        defaultAgent: 'build',
        defaultModel: '',
      }),
    }),
  );

  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/questions`),
    (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          {
            id: Q_ID,
            requestID: Q_ID,
            requestId: Q_ID,
            sessionID: MOCK_SESSION.id,
            questions: [
              {
                question: 'Instant question text?',
                header: 'Confirm',
                options: [
                  { label: 'Yes', description: '' },
                  { label: 'No', description: '' },
                ],
                multiple: false,
                custom: false,
              },
            ],
          },
        ]),
      }),
  );

  await page.route('/api/sessions*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([{ ...liveSession, pendingQuestion: false }]),
    }),
  );

  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}/events`), (route) =>
    route.abort(),
  );

  await page.goto(`/session/${MOCK_SESSION.id}`);
  await expect(page.locator('.session-layout')).toBeVisible();

  await expect(page.locator('.oc-question-box-text')).toContainText('Instant question text?', { timeout: 1_000 });
});

/**
 * Navigating from another session to one with a pending permission must
 * also show the prompt immediately — not only on a cold page load.
 */
test('permission prompt appears immediately when switching to a session with a pending permission', async ({ mockedPage: page }) => {
  const PERM_ID = 'perm-switch';
  const liveSessionA = {
    ...mockSessionWithLiveConnection(),
    id: MOCK_SESSION.id,
    title: MOCK_SESSION.title,
    pendingPermission: false,
  };
  const liveSessionB = {
    ...mockSessionWithLiveConnection(),
    id: MOCK_SESSION_2.id,
    title: MOCK_SESSION_2.title,
    pendingPermission: true,
  };

  // Session A — no prompt.
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ session: liveSessionA, messages: [], parts: [], totalMessages: 0, defaultAgent: 'build', defaultModel: '' }),
    }),
  );

  // Session B — has a pending permission.
  await page.route(new RegExp(`/api/session/${MOCK_SESSION_2.id}(\\?|$)`), (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ session: liveSessionB, messages: [], parts: [], totalMessages: 0, defaultAgent: 'build', defaultModel: '' }),
    }),
  );

  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION_2.id}/permissions`),
    (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          {
            id: PERM_ID,
            requestID: PERM_ID,
            sessionID: MOCK_SESSION_2.id,
            permission: 'Read /etc/hosts',
            patterns: [],
          },
        ]),
      }),
  );

  // Sidebar: neither session has the boolean set — isolates the fast path.
  await page.route('/api/sessions*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        { ...liveSessionA, pendingPermission: false },
        { ...liveSessionB, pendingPermission: false },
      ]),
    }),
  );

  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}/events`), (route) => route.abort());
  await page.route(new RegExp(`/api/session/${MOCK_SESSION_2.id}/events`), (route) => route.abort());

  // 1. Start on session A — no prompt.
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await expect(page.locator('.session-layout')).toBeVisible();
  await expect(page.locator('.oc-permission-wrap')).toHaveCount(0);

  // 2. Switch to session B.
  await page.locator('.session-sidebar-item', { hasText: MOCK_SESSION_2.title }).click();
  await expect(page.getByRole('heading', { name: new RegExp(MOCK_SESSION_2.title) })).toBeVisible({ timeout: 5_000 });

  // 3. The permission prompt must appear within 1 s of landing on session B.
  await expect(page.locator('.oc-permission-desc')).toContainText('/etc/hosts', { timeout: 1_000 });
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
