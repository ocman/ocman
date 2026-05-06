/**
 * e2e: Session detail page
 *
 * Covers:
 *  - Page renders with session title in the header breadcrumb
 *  - Sidebar shows "Recent sessions" heading
 *  - Other sessions from the mock list appear in the sidebar
 *  - Loading spinner shown while session data loads
 *  - Error banner shown when session fetch fails + Retry button
 *  - Navigating directly to a session URL works (deep-link)
 *  - Back navigation via header logo returns to dashboard
 *  - "New session" (+) button is visible
 *  - "Open in VS Code" button is visible
 *  - Archive button is present on sidebar session items
 *  - Sidebar item for the active session has aria-selected
 */

import { test, expect, MOCK_SESSION, MOCK_SESSION_2, mockSessionWithLiveConnection } from './fixtures';

const SESSION_URL = `/session/${MOCK_SESSION.id}`;

function buildSyntheticHistoricalThread(sessionId: string) {
  const messages: Array<Record<string, unknown>> = [];
  const parts: Array<Record<string, unknown>> = [];
  let ts = Date.now() - 60 * 60 * 1000;

  const addMessage = (message: Record<string, unknown>) => {
    messages.push(message);
  };
  const addPart = (part: Record<string, unknown>) => {
    parts.push(part);
  };

  const paragraphs = [
    'Investigating project layout and tracing navigation updates through the session detail page.',
    'Reviewing recent message and part mutations to understand why stale work survives route changes.',
    'Checking tool execution paths, status propagation, and sidebar synchronization behavior.',
  ];

  for (let i = 0; i < 28; i++) {
    const userId = `hist-user-${i}`;
    const asstId = `hist-asst-${i}`;
    const createdUser = ts;
    const createdAsst = ts + 2_000;

    addMessage({
      id: userId,
      sessionId,
      timeCreated: createdUser,
      timeUpdated: createdUser,
      data: {
        role: 'user',
        time: { created: createdUser },
      },
    });
    addPart({
      id: `part-${userId}`,
      sessionId,
      messageId: userId,
      timeCreated: createdUser,
      timeUpdated: createdUser,
      data: {
        type: 'text',
        text: `Historical user prompt ${i + 1}: please inspect the navigation behavior and summarize what you find.`,
      },
    });

    addMessage({
      id: asstId,
      sessionId,
      timeCreated: createdAsst,
      timeUpdated: createdAsst + 10_000,
      data: {
        role: 'assistant',
        finish: i % 3 === 0 ? 'stop' : 'tool-calls',
        time: { created: createdAsst, completed: createdAsst + 10_000 },
      },
    });
    addPart({
      id: `part-${asstId}-step-start`,
      sessionId,
      messageId: asstId,
      timeCreated: createdAsst,
      timeUpdated: createdAsst,
      data: { type: 'step-start', snapshot: `snapshot-${i}` },
    });
    addPart({
      id: `part-${asstId}-reasoning`,
      sessionId,
      messageId: asstId,
      timeCreated: createdAsst + 100,
      timeUpdated: createdAsst + 100,
      data: {
        type: 'reasoning',
        text: `**Reasoning block ${i + 1}**\n\n${paragraphs[i % paragraphs.length]}\n\n- compare route id\n- compare session object\n- verify stale work is ignored`,
      },
    });
    addPart({
      id: `part-${asstId}-tool`,
      sessionId,
      messageId: asstId,
      timeCreated: createdAsst + 300,
      timeUpdated: createdAsst + 1_000,
      data: {
        type: 'tool',
        tool: 'bash',
        callID: `call-${i}`,
        state: {
          status: 'completed',
          input: { command: `echo historical-run-${i} && printf '%s\\n' one two three` },
          metadata: {
            output: Array.from({ length: 10 }, (_, n) => `historical tool ${i} line ${n + 1}`).join('\n'),
            description: 'Synthetic historical completed tool call',
          },
        },
      },
    });
    addPart({
      id: `part-${asstId}-text`,
      sessionId,
      messageId: asstId,
      timeCreated: createdAsst + 1_500,
      timeUpdated: createdAsst + 2_000,
      data: {
        type: 'text',
        text: [
          `Historical assistant summary ${i + 1}.`,
          '',
          '```ts',
          'function summarizeNavigationIssue(currentId: string, targetId: string) {',
          '  return `${currentId} -> ${targetId}`',
          '}',
          '```',
        ].join('\n'),
      },
    });
    addPart({
      id: `part-${asstId}-step-finish`,
      sessionId,
      messageId: asstId,
      timeCreated: createdAsst + 2_500,
      timeUpdated: createdAsst + 2_500,
      data: {
        type: 'step-finish',
        reason: i % 3 === 0 ? 'stop' : 'tool-calls',
        snapshot: `snapshot-${i}`,
      },
    });

    ts += 30_000;
  }

  return { messages, parts };
}

// ---------------------------------------------------------------------------
// Navigation priority while streaming
// ---------------------------------------------------------------------------

/**
 * Install a browser-side EventSource stub that emits a slow assistant
 * text stream for `streamSessionId` forever (until close()). This is
 * intentionally not backed by a static HTTP SSE body: we need a live
 * stream that keeps producing deltas *while* the user clicks away, so
 * the test can expose the current bug where navigation waits for the
 * working session to reach a quieter/block boundary. The stub also
 * emits periodic `message.updated` events so the frontend takes its
 * real reconciliation path (`loadNow()`), which is closer to the
 * production behaviour than plain deltas alone.
 */
async function installSlowStreamingEventSource(
  page: import('@playwright/test').Page,
  streamSessionId: string,
) {
  await page.addInitScript(({ streamSessionId }) => {
    class StreamingEventSource {
      static CONNECTING = 0;
      static OPEN = 1;
      static CLOSED = 2;

      url: string;
      readyState = StreamingEventSource.CONNECTING;
      onopen: ((evt: Event) => void) | null = null;
      onerror: ((evt: Event) => void) | null = null;
      onmessage: ((evt: MessageEvent) => void) | null = null;
      listeners: Record<string, Array<(evt: Event) => void>> = {};
      private timer: number | null = null;
      private msgId = `msg-${streamSessionId}`;
      private partId = `part-${streamSessionId}`;
      private toolPartId = `tool-${streamSessionId}`;

      constructor(url: string) {
        this.url = url;
        // Only the active session's /events endpoint streams; any
        // other EventSource usage still opens cleanly but stays idle.
        queueMicrotask(() => {
          if (this.readyState === StreamingEventSource.CLOSED) return;
          this.readyState = StreamingEventSource.OPEN;
          this.onopen?.(new Event('open'));

          if (!url.includes(`/api/session/${streamSessionId}/events`)) return;

          const created = JSON.stringify({
            type: 'message.created',
            properties: {
              sessionID: streamSessionId,
              info: {
                id: this.msgId,
                sessionID: streamSessionId,
                role: 'assistant',
                time: { created: Date.now() },
              },
              parts: [
                { id: this.partId, type: 'text', text: '' },
                {
                  id: this.toolPartId,
                  type: 'tool',
                  tool: 'bash',
                  callID: `call-${streamSessionId}`,
                  state: {
                    status: 'running',
                    input: { command: 'printf "streaming tool\\n"' },
                    metadata: {
                      output: 'streaming tool boot',
                      description: 'Synthetic active tool call',
                    },
                  },
                },
              ],
            },
          });
          this.onmessage?.(new MessageEvent('message', { data: created }));

          let i = 0;
          const codeLine = 'const veryLongVariableName = 1234567890; // streamed code\n';
          this.timer = window.setInterval(() => {
            if (this.readyState === StreamingEventSource.CLOSED) return;
            i += 1;
            const delta = JSON.stringify({
              type: 'message.part.delta',
              properties: {
                sessionID: streamSessionId,
                messageID: this.msgId,
                partID: this.partId,
                field: 'text',
                // Large-ish markdown/code deltas to exercise the real
                // text-part rendering path rather than a tiny token.
                delta: i === 1
                  ? 'Streaming response start\n\n```ts\n'
                  : `${codeLine}${codeLine}${codeLine}// chunk-${i}\n`,
              },
            });
            this.onmessage?.(new MessageEvent('message', { data: delta }));

            if (i % 2 === 0) {
              const toolUpdate = JSON.stringify({
                type: 'message.part.updated',
                properties: {
                  sessionID: streamSessionId,
                  part: {
                    id: this.toolPartId,
                    messageID: this.msgId,
                    sessionID: streamSessionId,
                    type: 'tool',
                    tool: 'bash',
                    callID: `call-${streamSessionId}`,
                    state: {
                      status: i % 6 === 0 ? 'completed' : 'running',
                      input: { command: 'printf "streaming tool\\n"' },
                      metadata: {
                        output: Array.from({ length: 8 }, (_, n) => `tool chunk ${i}.${n + 1}`).join('\n'),
                        description: 'Synthetic active tool call',
                      },
                    },
                  },
                },
              });
              this.onmessage?.(new MessageEvent('message', { data: toolUpdate }));
            }

            // Every few chunks emit message.updated too. In the real
            // app this triggers the reconciliation/load path, which is
            // one of the suspected contributors to navigation delay.
            if (i % 3 === 0) {
              const updated = JSON.stringify({
                type: 'message.updated',
                properties: {
                  sessionID: streamSessionId,
                  info: {
                    id: this.msgId,
                    sessionID: streamSessionId,
                    role: 'assistant',
                    tokens: { output: i * 10 },
                    time: { created: Date.now() - 100 },
                  },
                },
              });
              this.onmessage?.(new MessageEvent('message', { data: updated }));
            }
          }, 40);
        });
      }

      addEventListener(name: string, cb: (evt: Event) => void) {
        (this.listeners[name] ||= []).push(cb);
      }

      close() {
        this.readyState = StreamingEventSource.CLOSED;
        if (this.timer !== null) window.clearInterval(this.timer);
      }
    }

    Object.defineProperty(window, 'EventSource', {
      configurable: true,
      writable: true,
      value: StreamingEventSource,
    });
  }, { streamSessionId });
}

async function setupStreamingSessionPage(page: import('@playwright/test').Page) {
  const liveSession = mockSessionWithLiveConnection({
    ...MOCK_SESSION,
    status: 'busy',
    liveConnection: true,
  });
  const historical = buildSyntheticHistoricalThread(MOCK_SESSION.id);

  await installSlowStreamingEventSource(page, MOCK_SESSION.id);

  await page.route('/api/sessions*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([liveSession, MOCK_SESSION_2]),
    }),
  );

  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), async (route) => {
    // Add a little latency so repeated message.updated-triggered
    // reconciliations overlap while the user clicks away.
    await new Promise((resolve) => setTimeout(resolve, 150));
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        session: liveSession,
        messages: historical.messages,
        parts: historical.parts,
        totalMessages: historical.messages.length,
        defaultAgent: 'build',
        defaultModel: 'anthropic/claude-3-5-sonnet-20241022',
      }),
    });
  });

  await page.goto(SESSION_URL);
  await expect(page.getByTestId('session-layout')).toBeVisible();
  // Prove the stream is actively appending while we prepare to
  // navigate away.
  await expect(page.locator('.oc-msg-assistant').filter({ hasText: 'Streaming response start' })).toHaveCount(1, { timeout: 5_000 });
  // Let a few delta/update cycles land so the page is genuinely hot.
  await page.waitForTimeout(400);
}

// ---------------------------------------------------------------------------
// Basic render
// ---------------------------------------------------------------------------

test('session detail page renders without crashing', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  await expect(page.getByTestId('session-layout')).toBeVisible();
});

test('shows loading spinner while session data is loading', async ({ mockedPage: page }) => {
  let resolveSession!: () => void;
  const sessionReadyP = new Promise<void>((resolve) => { resolveSession = resolve; });

  // Use a regex so the route matches the session URL with query parameters
  // (e.g. ?limit=30&offset=0) that the frontend appends. A plain string
  // pattern only matches the exact path and misses the actual request.
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}`), async (route) => {
    const url = route.request().url();
    if (url.includes('/agents') || url.includes('/commands') || url.includes('/models') ||
        url.includes('/permissions') || url.includes('/questions') || url.includes('/events')) {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) });
      return;
    }
    await sessionReadyP;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        session: MOCK_SESSION,
        messages: [],
        parts: [],
        totalMessages: 0,
        defaultAgent: '',
        defaultModel: '',
      }),
    });
  });

  // Use waitUntil:'commit' so the navigation resolves as soon as the
  // response headers arrive, giving the expect a chance to observe the
  // loading spinner before the delayed session response resolves.
  await page.goto(SESSION_URL, { waitUntil: 'commit' });
  await expect(page.getByTestId('loading-spinner')).toBeVisible({ timeout: 5_000 });
  resolveSession();
});

test('shows error banner when session fetch fails', async ({ mockedPage: page }) => {
  // Override the session detail fetch to return 500. We use a regex to match
  // the base session URL with any query string but exclude sub-paths like /agents.
  await page.route(
    new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`),
    (route) => route.fulfill({ status: 500, body: 'Internal Server Error' }),
  );

  await page.goto(SESSION_URL);
  await expect(page.getByTestId('error-banner')).toBeVisible({ timeout: 5_000 });
  await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible();
});

// ---------------------------------------------------------------------------
// Header breadcrumb
// ---------------------------------------------------------------------------

test('session title appears in header breadcrumb', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  await expect(page.getByRole('banner')).toContainText(MOCK_SESSION.title, { timeout: 5_000 });
});

test('header logo links back to dashboard', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  await page.getByRole('heading', { level: 1 }).getByRole('link', { name: 'ocman' }).click();
  await expect(page).toHaveURL('/');
});

// ---------------------------------------------------------------------------
// Sidebar
// ---------------------------------------------------------------------------

test('sidebar shows "Recent sessions" heading', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  await expect(page.getByTestId('sidebar-heading')).toContainText('Recent sessions');
});

test('sidebar shows both mock sessions', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  await expect(page.getByRole('button', { name: /Fix the login bug/ })).toBeVisible({ timeout: 5_000 });
  await expect(page.getByRole('button', { name: /Refactor auth module/ })).toBeVisible({ timeout: 5_000 });
});

test('active session sidebar item is aria-selected', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  const activeItem = page.locator('[aria-selected="true"]', { hasText: 'Fix the login bug' });
  await expect(activeItem).toBeVisible({ timeout: 5_000 });
});

test('clicking a sidebar session navigates to that session', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  await page.getByRole('button', { name: /Refactor auth module/ }).click();
  await expect(page).toHaveURL(`/session/${MOCK_SESSION_2.id}`);
});

test('sidebar archive button is visible on session items', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  const archiveBtns = page.getByRole('button', { name: 'Archive session' });
  await expect(archiveBtns.first()).toBeVisible({ timeout: 5_000 });
});

// ---------------------------------------------------------------------------
// Session action buttons
// ---------------------------------------------------------------------------

test('"New session" button is visible in session detail', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  await expect(page.getByRole('button', { name: 'New session' })).toBeVisible({ timeout: 5_000 });
});

test('"Open in VS Code" button is visible', async ({ mockedPage: page }) => {
  await page.goto(SESSION_URL);
  await expect(page.getByRole('button', { name: /Open in VS Code/ })).toBeVisible({ timeout: 5_000 });
});

// ---------------------------------------------------------------------------
// Deep-link navigation
// ---------------------------------------------------------------------------

test('navigating directly to a session URL loads the correct session', async ({
  mockedPage: page,
}) => {
  await page.goto(`/session/${MOCK_SESSION_2.id}`);
  await expect(page.getByTestId('session-layout')).toBeVisible();
  await expect(page.getByRole('banner')).toContainText(MOCK_SESSION_2.title, { timeout: 5_000 });
});

test('unknown session ID shows an error', async ({ mockedPage: page }) => {
  // Override the session detail fetch for the unknown ID to return 404.
  await page.route(
    new RegExp('/api/session/nonexistent-id(\\?|$)'),
    (route) => route.fulfill({ status: 404, body: 'Not Found' }),
  );

  await page.goto('/session/nonexistent-id');
  await expect(page.getByTestId('error-banner')).toBeVisible({ timeout: 5_000 });
});

test('navigating to another session during text streaming shows the target session immediately', async ({ mockedPage: page }) => {
  await setupStreamingSessionPage(page);

  await expect(page.locator('.oc-msg-assistant').filter({ hasText: 'chunk-' })).toHaveCount(1, { timeout: 5_000 });

  await page.getByRole('button', { name: /Refactor auth module/ }).click();

  // This should be effectively instant. Today the app often stays on
  // the streaming session until the worker reaches a quieter/block
  // boundary, so this assertion is expected to FAIL until the bug is
  // fixed.
  await expect(page).toHaveURL(`/session/${MOCK_SESSION_2.id}`, { timeout: 500 });
  await expect(page.getByRole('banner')).toContainText(MOCK_SESSION_2.title, { timeout: 500 });
  await expect(page.locator('.oc-msg-assistant').filter({ hasText: 'Streaming response start' })).toHaveCount(0, { timeout: 500 });
});

test('navigating to another session during an active tool phase shows the target session immediately', async ({ mockedPage: page }) => {
  await setupStreamingSessionPage(page);

  await expect(page.getByText('Synthetic active tool call')).toBeVisible({ timeout: 5_000 });

  await page.getByRole('button', { name: /Refactor auth module/ }).click();

  await expect(page).toHaveURL(`/session/${MOCK_SESSION_2.id}`, { timeout: 500 });
  await expect(page.getByRole('banner')).toContainText(MOCK_SESSION_2.title, { timeout: 500 });
  await expect(page.getByText('Synthetic active tool call')).toHaveCount(0, { timeout: 500 });
});

test('navigating back to dashboard while the current session is streaming is immediate', async ({ mockedPage: page }) => {
  await setupStreamingSessionPage(page);

  await page.getByRole('heading', { level: 1 }).getByRole('link', { name: 'ocman' }).click();

  // Same regression as above, but through the dashboard link. This
  // should switch immediately even while SSE deltas keep flowing.
  await expect(page).toHaveURL('/', { timeout: 500 });
});
