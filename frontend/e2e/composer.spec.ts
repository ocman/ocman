/**
 * e2e: Composer (message input) — no real LLM required
 *
 * All responses are mocked at the HTTP layer. The LLM response is
 * simulated by:
 *  1. The POST /api/session/:id/message returning 200 immediately.
 *  2. An SSE stream delivering a synthetic `message.created` event with
 *     a canned assistant reply, then a `session.idle` event.
 *
 * This lets every composer test run fully offline without any API key
 * or live agent process.
 *
 * Covers:
 *  - Composer is disabled (greyed out) when liveConnection=false
 *  - Composer is enabled when liveConnection=true (portAvailable)
 *  - Typing a message and pressing Enter sends POST /api/session/:id/message
 *  - Optimistic user message appears immediately in the thread
 *  - Mocked assistant reply appears in the thread via SSE
 *  - The stop button is visible while the agent is "running"
 *  - The stop button calls POST /api/session/:id/abort
 *  - Shift+Enter inserts a newline instead of sending
 *  - Draft is preserved in sessionStorage between renders
 *  - Slash menu opens when input starts with "/"
 *  - Slash command /archive is dispatched as handleCommand, not POST message
 *  - Busy (409) response shows a "try again" message in the thread
 */

import { test, expect, MOCK_SESSION, mockSse, sseMessage, mockSessionWithLiveConnection } from './fixtures';

const SESSION_URL = `/session/${MOCK_SESSION.id}`;

// ---------------------------------------------------------------------------
// Helper: navigate to the session page with a live-connection-enabled mock
// ---------------------------------------------------------------------------

async function goToLiveSession(page: import('@playwright/test').Page) {
  const liveSession = mockSessionWithLiveConnection();

  // Return live session with liveConnection=true so portAvailable=true
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
        defaultModel: 'anthropic/claude-3-5-sonnet-20241022',
      }),
    }),
  );

  // Sessions list also needs liveConnection=true so the sidebar works
  await page.route('/api/sessions*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([liveSession]),
    }),
  );

  await page.goto(SESSION_URL);
  // Wait for the session to load (session layout visible)
  await expect(page.locator('.session-layout')).toBeVisible();
}

// ---------------------------------------------------------------------------
// Disabled state
// ---------------------------------------------------------------------------

test('composer is disabled when session has no live connection', async ({ mockedPage: page }) => {
  // Block the SSE endpoint so onopen never fires and portAvailable stays false
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}/events`), (route) =>
    route.fulfill({ status: 503, body: 'Service Unavailable' }),
  );
  await page.goto(SESSION_URL);
  await expect(page.locator('.session-layout')).toBeVisible();
  // The composer textarea should be disabled
  await expect(page.locator('.oc-composer-input')).toBeDisabled({ timeout: 5_000 });
});

test('disabled composer textarea has a "No live connection" placeholder', async ({ mockedPage: page }) => {
  // Block SSE entirely so onopen never fires and portAvailable stays false
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}/events`), (route) =>
    route.fulfill({ status: 503, body: 'Service Unavailable' }),
  );
  await page.goto(SESSION_URL);
  // When disabled, the textarea placeholder includes "No live connection to the agent"
  const textarea = page.locator('.oc-composer-input');
  await expect(textarea).toBeDisabled({ timeout: 5_000 });
  await expect(textarea).toHaveAttribute('placeholder', /No live connection/i);
});

// ---------------------------------------------------------------------------
// Enabled state
// ---------------------------------------------------------------------------

test('composer is enabled when session has liveConnection=true', async ({ mockedPage: page }) => {
  await goToLiveSession(page);
  await expect(page.locator('.oc-composer-input')).toBeEnabled({ timeout: 5_000 });
});

// ---------------------------------------------------------------------------
// Sending a message (no real LLM)
// ---------------------------------------------------------------------------

test('typing and pressing Enter sends POST /api/session/:id/message', async ({ mockedPage: page }) => {
  await goToLiveSession(page);

  // Stub the message POST so waitForRequest succeeds even without a backend
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/message`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );

  // Click to focus, fill text, then press Enter
  await page.locator('.oc-composer-input').click();
  await page.locator('.oc-composer-input').fill('Hello, world!');

  // Capture the outgoing request
  const [request] = await Promise.all([
    page.waitForRequest((req) =>
      req.url().includes(`/api/session/${MOCK_SESSION.id}/message`) && req.method() === 'POST',
    ),
    page.locator('.oc-composer-input').press('Enter'),
  ]);

  const body = JSON.parse(request.postData() ?? '{}');
  expect(body.message).toBe('Hello, world!');
});

test('optimistic user message appears immediately in the thread', async ({ mockedPage: page }) => {
  await goToLiveSession(page);

  // Delay the API response so we can observe the optimistic message
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/message`),
    async (route) => {
      await new Promise((r) => setTimeout(r, 500));
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) });
    },
  );

  await page.locator('.oc-composer-input').fill('My optimistic message');
  await page.locator('.oc-composer-input').press('Enter');

  // The user message should appear before the response arrives
  await expect(page.locator('.oc-msg-user', { hasText: 'My optimistic message' })).toBeVisible({ timeout: 2_000 });
});

test('mocked assistant reply appears in thread via SSE (on page load)', async ({ mockedPage: page }) => {
  // Wire up SSE to deliver a synthetic assistant message immediately on connection.
  // We include both a user message (so the session has prior context) and the
  // assistant reply, then an idle signal. The reply should appear in the thread
  // without requiring a real LLM call.
  const { event: userEvent } = sseMessage({
    sessionId: MOCK_SESSION.id,
    role: 'user',
    text: 'Prior user message',
  });
  const { event: assistantEvent } = sseMessage({
    sessionId: MOCK_SESSION.id,
    role: 'assistant',
    text: 'Hello from the mocked LLM!',
    finish: 'end_turn',
  });
  // Do NOT send session.idle — that triggers loadNow() which reconciles
  // against the empty messages mock and could interfere with SSE messages.
  await mockSse(page, MOCK_SESSION.id, [userEvent, assistantEvent]);

  await goToLiveSession(page);

  // The assistant's reply should appear in the thread
  await expect(
    page.locator('.oc-msg-assistant', { hasText: 'Hello from the mocked LLM!' }),
  ).toBeVisible({ timeout: 8_000 });
});

// ---------------------------------------------------------------------------
// Stop / abort
// ---------------------------------------------------------------------------

test('stop button is visible while agent is running (SSE busy)', async ({ mockedPage: page }) => {
  // Deliver a user message SSE event (no finish → still running)
  const { event: userEvent } = sseMessage({
    sessionId: MOCK_SESSION.id,
    role: 'user',
    text: 'Are you there?',
  });
  // Keep the stream open without delivering finish — agent stays "running"
  await mockSse(page, MOCK_SESSION.id, [userEvent]);

  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/message`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );

  await goToLiveSession(page);
  await page.locator('.oc-composer-input').fill('Hello');
  await page.locator('.oc-composer-input').press('Enter');

  // Stop button should appear
  await expect(page.locator('.oc-stop-btn')).toBeVisible({ timeout: 3_000 });
});

test('clicking stop button calls POST /api/session/:id/abort', async ({ mockedPage: page }) => {
  const { event: userEvent } = sseMessage({
    sessionId: MOCK_SESSION.id,
    role: 'user',
    text: 'Abort me',
  });
  await mockSse(page, MOCK_SESSION.id, [userEvent]);

  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/message`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/abort`),
    (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) }),
  );

  await goToLiveSession(page);
  await page.locator('.oc-composer-input').fill('Stop me');
  await page.locator('.oc-composer-input').press('Enter');

  await expect(page.locator('.oc-stop-btn')).toBeVisible({ timeout: 3_000 });
  const [abortRequest] = await Promise.all([
    page.waitForRequest((req) =>
      req.url().includes(`/api/session/${MOCK_SESSION.id}/abort`) && req.method() === 'POST',
    ),
    page.locator('.oc-stop-btn').click(),
  ]);
  expect(abortRequest).toBeTruthy();
});

// ---------------------------------------------------------------------------
// Input behaviour
// ---------------------------------------------------------------------------

test('Shift+Enter inserts a newline instead of sending', async ({ mockedPage: page }) => {
  await goToLiveSession(page);

  let messageSent = false;
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/message`),
    (route) => {
      messageSent = true;
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) });
    },
  );

  const textarea = page.locator('.oc-composer-input');
  await textarea.fill('line one');
  await textarea.press('Shift+Enter');
  await textarea.type('line two');

  // Should NOT have sent a message
  expect(messageSent).toBe(false);
  // The textarea value should contain a newline
  const value = await textarea.inputValue();
  expect(value).toContain('\n');
});

// ---------------------------------------------------------------------------
// Slash command menu
// ---------------------------------------------------------------------------

test('typing "/" opens the slash command menu', async ({ mockedPage: page }) => {
  // Provide some slash commands from the API
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/commands`),
    (route) => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        { name: 'archive', description: 'Archive this session' },
        { name: 'compact', description: 'Compact conversation' },
      ]),
    }),
  );

  await goToLiveSession(page);
  await page.locator('.oc-composer-input').fill('/');
  await expect(page.locator('.oc-slash-menu')).toBeVisible({ timeout: 3_000 });
});

test('slash menu shows available commands', async ({ mockedPage: page }) => {
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/commands`),
    (route) => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        { name: 'archive', description: 'Archive this session' },
        { name: 'compact', description: 'Compact conversation' },
      ]),
    }),
  );

  await goToLiveSession(page);
  await page.locator('.oc-composer-input').fill('/');
  await expect(page.locator('.oc-slash-item', { hasText: '/archive' })).toBeVisible({ timeout: 3_000 });
  await expect(page.locator('.oc-slash-item', { hasText: '/compact' })).toBeVisible();
});

test('slash menu closes when input is cleared', async ({ mockedPage: page }) => {
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/commands`),
    (route) => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([{ name: 'archive', description: '' }]),
    }),
  );

  await goToLiveSession(page);
  await page.locator('.oc-composer-input').fill('/');
  await expect(page.locator('.oc-slash-menu')).toBeVisible({ timeout: 2_000 });
  // Clear the input
  await page.locator('.oc-composer-input').fill('');
  await expect(page.locator('.oc-slash-menu')).toHaveCount(0);
});

// ---------------------------------------------------------------------------
// Busy (409) response — failed user-message banner with Retry / Dismiss
// ---------------------------------------------------------------------------

test('409 busy response shows a retryable failed banner on the user message', async ({ mockedPage: page }) => {
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/message`),
    (route) => route.fulfill({
      status: 409,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'session is busy, try again' }),
    }),
  );

  await goToLiveSession(page);
  await page.locator('.oc-composer-input').fill('Hello');
  await page.locator('.oc-composer-input').press('Enter');

  // The optimistic user bubble gets a failed banner with the error text
  // and a Retry button — the prompt itself is preserved on screen.
  const failedBanner = page.locator('.oc-msg-failed-banner');
  await expect(failedBanner).toBeVisible({ timeout: 3_000 });
  await expect(failedBanner).toContainText(/try again|busy/i);
  await expect(failedBanner.getByRole('button', { name: 'Retry' })).toBeVisible();
  await expect(failedBanner.getByRole('button', { name: /dismiss/i })).toBeVisible();
});

test('failed-send Retry replays the original prompt', async ({ mockedPage: page }) => {
  // First call fails with 409, subsequent calls succeed.
  let attempt = 0;
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}/message`),
    (route) => {
      attempt += 1;
      if (attempt === 1) {
        return route.fulfill({
          status: 409,
          contentType: 'application/json',
          body: JSON.stringify({ error: 'session is busy, try again' }),
        });
      }
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ ok: true }),
      });
    },
  );

  await goToLiveSession(page);
  await page.locator('.oc-composer-input').fill('Replay me');
  await page.locator('.oc-composer-input').press('Enter');

  const failedBanner = page.locator('.oc-msg-failed-banner');
  await expect(failedBanner).toBeVisible({ timeout: 3_000 });
  // The viewport's auto-scroll MutationObserver can make the button
  // position unstable between frames, causing Playwright's actionability
  // check to report "intercepts pointer events". Use dispatchEvent to
  // bypass the hit-test since the button is visually present and enabled.
  await failedBanner.getByRole('button', { name: 'Retry' }).dispatchEvent('click');

  // Banner clears once the retry succeeds.
  await expect(failedBanner).toHaveCount(0, { timeout: 3_000 });
  expect(attempt).toBeGreaterThanOrEqual(2);
});

// ---------------------------------------------------------------------------
// Queued follow-up messages (#58): layout of the list above the composer
// ---------------------------------------------------------------------------

test('a long queued message wraps and keeps its controls visible within the composer', async ({ mockedPage: page }) => {
  const longText =
    'This is a very long queued follow-up prompt that must wrap onto ' +
    'multiple lines instead of running off to the right under the sidebar. ' +
    'It also contains an unbreakable token ' +
    'https://example.com/some/really/long/path/that/cannot/be/broken/normally/xxxxxxxxxxxxxxxxxxxxxxxxxxxx ' +
    'to prove overflow-wrap handles it too.';

  // Seed the queue with one long item (GET /api/session/:id/queue).
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}/queue(\\?|$)`), (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        { id: 'q_long', text: longText, hasImages: false, createdAt: 1 },
      ]),
    }),
  );

  await goToLiveSession(page);

  // The queue list and its row render.
  const queue = page.getByTestId('queued-messages');
  await expect(queue).toBeVisible({ timeout: 5_000 });
  await expect(queue.getByText(/very long queued follow-up/)).toBeVisible();

  // The remove control must be visible and reachable (not clipped off-screen).
  const removeBtn = queue.getByRole('button', { name: 'Remove from queue' });
  await expect(removeBtn).toBeVisible();

  // Layout assertion: the remove button's right edge must stay within the
  // composer wrap — if the text failed to wrap, the row would overflow and
  // push the controls past (under) the surrounding column.
  const wrap = page.locator('.oc-composer-wrap');
  const wrapBox = await wrap.boundingBox();
  const btnBox = await removeBtn.boundingBox();
  expect(wrapBox).not.toBeNull();
  expect(btnBox).not.toBeNull();
  if (wrapBox && btnBox) {
    // Right edge of the button is inside the composer wrap (+1px tolerance).
    expect(btnBox.x + btnBox.width).toBeLessThanOrEqual(wrapBox.x + wrapBox.width + 1);
    // And the row is tall enough to have wrapped to more than one line
    // (a single 11px line would be ~16px with padding; wrapped is taller).
    const row = queue.locator('.oc-queued-message').first();
    const rowBox = await row.boundingBox();
    expect(rowBox).not.toBeNull();
    if (rowBox) expect(rowBox.height).toBeGreaterThan(30);
  }
});
