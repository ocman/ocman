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

  // Explicitly mock session 2 with no delay so navigation to it
  // resolves immediately and the 500 ms header assertions are reliable.
  await page.route(new RegExp(`/api/session/${MOCK_SESSION_2.id}(\\?|$)`), (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        session: MOCK_SESSION_2,
        messages: [],
        parts: [],
        totalMessages: 0,
        defaultAgent: '',
        defaultModel: '',
      }),
    }),
  );

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

test('shows loading skeleton while session data is loading', async ({ mockedPage: page }) => {
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
  await expect(page.getByTestId('thread-skeleton')).toBeVisible({ timeout: 5_000 });
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

  // URL change is synchronous (flushSync); content update needs a
  // fetch round-trip that can take longer on slow CI runners.
  await expect(page).toHaveURL(`/session/${MOCK_SESSION_2.id}`, { timeout: 2_000 });
  await expect(page.getByRole('banner')).toContainText(MOCK_SESSION_2.title, { timeout: 2_000 });
  await expect(page.locator('.oc-msg-assistant').filter({ hasText: 'Streaming response start' })).toHaveCount(0, { timeout: 2_000 });
});

test('navigating to another session during an active tool phase shows the target session immediately', async ({ mockedPage: page }) => {
  await setupStreamingSessionPage(page);

  await expect(page.getByText('Synthetic active tool call')).toBeVisible({ timeout: 5_000 });

  await page.getByRole('button', { name: /Refactor auth module/ }).click();

  await expect(page).toHaveURL(`/session/${MOCK_SESSION_2.id}`, { timeout: 2_000 });
  await expect(page.getByRole('banner')).toContainText(MOCK_SESSION_2.title, { timeout: 2_000 });
  await expect(page.getByText('Synthetic active tool call')).toHaveCount(0, { timeout: 2_000 });
});

test('navigating back to dashboard while the current session is streaming is immediate', async ({ mockedPage: page }) => {
  await setupStreamingSessionPage(page);

  await page.getByRole('heading', { level: 1 }).getByRole('link', { name: 'ocman' }).click();

  // Same regression as above, but through the dashboard link. This
  // should switch immediately even while SSE deltas keep flowing.
  await expect(page).toHaveURL('/', { timeout: 500 });
});

// ---------------------------------------------------------------------------
// Regression: streaming counter (live SSE deltas during a turn)
// ---------------------------------------------------------------------------
//
// Reproducer for the user-reported regression "tool/text blocks don't
// show until refresh". A counter stream sends `message.part.delta`
// events at 1-second intervals carrying the text "1", "2", ..., "5".
// Each number must be visible in the DOM *while the stream is still
// running* — never after a refresh, never only after the final event.
//
// This is the most surgical streaming test we have: minimal payload,
// no tool parts, no embedded `parts` snapshot, just deltas accruing
// on a single text part. If text deltas don't render live, every
// other streaming surface (tool output, edit diffs, etc.) breaks the
// same way.
test('streaming counter renders each number live as deltas arrive', async ({ mockedPage: page }) => {
  const streamSessionId = MOCK_SESSION.id;
  const liveSession = mockSessionWithLiveConnection({
    ...MOCK_SESSION,
    status: 'busy',
    liveConnection: true,
  });

  // Install a fake EventSource that emits one delta per second for
  // the numbers 1..5. The delta accumulates onto a single text part
  // already created by `message.created`. After the last number,
  // emit `session.idle` so the session transitions out of busy —
  // but the test asserts each number appears *before* idle lands.
  await page.addInitScript(({ streamSessionId }) => {
    class CounterEventSource {
      static CONNECTING = 0;
      static OPEN = 1;
      static CLOSED = 2;

      url: string;
      readyState = CounterEventSource.CONNECTING;
      onopen: ((evt: Event) => void) | null = null;
      onerror: ((evt: Event) => void) | null = null;
      onmessage: ((evt: MessageEvent) => void) | null = null;
      listeners: Record<string, Array<(evt: Event) => void>> = {};
      private intervalId: number | null = null;
      private msgId = `msg-counter-${streamSessionId}`;
      private partId = `part-counter-${streamSessionId}`;

      constructor(url: string) {
        this.url = url;
        queueMicrotask(() => {
          if (this.readyState === CounterEventSource.CLOSED) return;
          this.readyState = CounterEventSource.OPEN;
          this.onopen?.(new Event('open'));

          if (!url.includes(`/api/session/${streamSessionId}/events`)) return;

          // Seed an assistant message with one empty text part.
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
              ],
            },
          });
          this.onmessage?.(new MessageEvent('message', { data: created }));

          // Stream the counter: one delta per second carrying the
          // next number. The user must see each number appear in
          // real time, not at the end.
          let i = 0;
          const COUNT_TO = 5;
          // Use a short interval so the e2e doesn't take a full
          // 5 seconds. The contract we're testing is "delta lands
          // → DOM shows" regardless of cadence; 100ms is plenty
          // to expose the bug.
          const INTERVAL_MS = 100;
          this.intervalId = window.setInterval(() => {
            if (this.readyState === CounterEventSource.CLOSED) return;
            i += 1;
            const delta = JSON.stringify({
              type: 'message.part.delta',
              properties: {
                sessionID: streamSessionId,
                messageID: this.msgId,
                partID: this.partId,
                field: 'text',
                // Each delta appends one number + space so the
                // accumulated text is "1 2 3 4 5 ".
                delta: `${i} `,
              },
            });
            this.onmessage?.(new MessageEvent('message', { data: delta }));
            if (i >= COUNT_TO) {
              window.clearInterval(this.intervalId!);
              this.intervalId = null;
              // Emit session.idle so the test exercises the
              // end-of-turn refetch path too. Before the
              // reconcile-mode fix, this refetch was a wholesale
              // replace — when the mocked API response returned
              // empty messages/parts (server hadn't caught up
              // with the SSE stream), it wiped the streamed
              // numbers from the DOM. The reconcile mode keeps
              // in-memory state alive when the server's response
              // doesn't yet include it.
              const idle = JSON.stringify({
                type: 'session.idle',
                properties: { sessionID: streamSessionId },
              });
              this.onmessage?.(new MessageEvent('message', { data: idle }));
            }
          }, INTERVAL_MS);
        });
      }

      addEventListener(name: string, cb: (evt: Event) => void) {
        (this.listeners[name] ||= []).push(cb);
      }

      close() {
        this.readyState = CounterEventSource.CLOSED;
        if (this.intervalId !== null) window.clearInterval(this.intervalId);
      }
    }

    Object.defineProperty(window, 'EventSource', {
      configurable: true,
      writable: true,
      value: CounterEventSource,
    });
  }, { streamSessionId });

  await page.route('/api/sessions*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([liveSession, MOCK_SESSION_2]),
    }),
  );

  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), async (route) => {
    await route.fulfill({
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
    });
  });

  await page.goto(SESSION_URL);
  await expect(page.getByTestId('session-layout')).toBeVisible();

  // Each number must appear *while* the stream is still running.
  // The whole stream takes ~500ms (5 numbers × 100ms), so we give
  // each assertion 3s of headroom while the rest of the stream
  // continues to land.
  const assistantBubble = page.locator('.oc-msg-assistant');
  for (let n = 1; n <= 5; n++) {
    await expect(assistantBubble).toContainText(String(n), { timeout: 3_000 });
  }

  // Once all 5 numbers have streamed, the full accumulated text
  // should be visible. We check that no number was dropped.
  await expect(assistantBubble).toContainText('1 2 3 4 5');
});

test('long-running tool call renders partial output before completion', async ({ mockedPage: page }) => {
  const streamSessionId = MOCK_SESSION.id;
  const liveSession = mockSessionWithLiveConnection({
    ...MOCK_SESSION,
    status: 'busy',
    liveConnection: true,
  });

  await page.addInitScript(({ streamSessionId }) => {
    class LongToolEventSource {
      static CONNECTING = 0;
      static OPEN = 1;
      static CLOSED = 2;

      url: string;
      readyState = LongToolEventSource.CONNECTING;
      onopen: ((evt: Event) => void) | null = null;
      onerror: ((evt: Event) => void) | null = null;
      onmessage: ((evt: MessageEvent) => void) | null = null;
      listeners: Record<string, Array<(evt: MessageEvent) => void>> = {};
      private timers: number[] = [];

      constructor(url: string) {
        this.url = url;
        queueMicrotask(() => {
          if (this.readyState === LongToolEventSource.CLOSED) return;
          this.readyState = LongToolEventSource.OPEN;
          this.onopen?.(new Event('open'));
          if (!url.includes(`/api/session/${streamSessionId}/events`)) return;

          const messageId = `msg-long-tool-${streamSessionId}`;
          const partId = `part-long-tool-${streamSessionId}`;

          // Exercise the raw named `tool` channel shape: if the app
          // drops this start event, no tool block is visible until
          // the completed snapshot arrives at the end of the command.
          this.dispatchNamed('tool', {
            id: partId,
            messageID: messageId,
            sessionID: streamSessionId,
            type: 'tool',
            tool: 'bash',
            state: {
              status: 'running',
              input: { command: 'for i in 1 2 3 4 5; do echo tool-second-$i; sleep 1; done' },
              output: '',
            },
          });

          for (let i = 1; i <= 5; i += 1) {
            this.timers.push(window.setTimeout(() => {
              if (this.readyState === LongToolEventSource.CLOSED) return;
              this.dispatchDefault({
                type: 'message.part.delta',
                properties: {
                  sessionID: streamSessionId,
                  messageID: messageId,
                  partID: partId,
                  field: 'state.output',
                  delta: `tool-second-${i}\n`,
                },
              });
            }, i * 1_000));
          }

          this.timers.push(window.setTimeout(() => {
            if (this.readyState === LongToolEventSource.CLOSED) return;
            this.dispatchNamed('message.part.updated', {
              id: partId,
              messageID: messageId,
              sessionID: streamSessionId,
              type: 'tool',
              tool: 'bash',
              state: {
                status: 'completed',
                input: { command: 'for i in 1 2 3 4 5; do echo tool-second-$i; sleep 1; done' },
                output: 'tool-second-1\ntool-second-2\ntool-second-3\ntool-second-4\ntool-second-5\n',
              },
            });
            this.dispatchDefault({ type: 'session.idle', properties: { sessionID: streamSessionId } });
          }, 5_500));
        });
      }

      addEventListener(name: string, cb: (evt: MessageEvent) => void) {
        (this.listeners[name] ||= []).push(cb);
      }

      close() {
        this.readyState = LongToolEventSource.CLOSED;
        for (const timer of this.timers) window.clearTimeout(timer);
        this.timers = [];
      }

      private dispatchDefault(payload: Record<string, unknown>) {
        const event = new MessageEvent('message', { data: JSON.stringify(payload) });
        this.onmessage?.(event);
      }

      private dispatchNamed(name: string, payload: Record<string, unknown>) {
        const event = new MessageEvent(name, { data: JSON.stringify(payload) });
        for (const cb of this.listeners[name] || []) cb(event);
      }
    }

    Object.defineProperty(window, 'EventSource', {
      configurable: true,
      writable: true,
      value: LongToolEventSource,
    });
  }, { streamSessionId });

  await page.route('/api/sessions*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([liveSession, MOCK_SESSION_2]),
    }),
  );

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

  await page.goto(SESSION_URL);
  await expect(page.getByTestId('session-layout')).toBeVisible();

  await expect(page.getByText('tool-second-1')).toBeVisible({ timeout: 4_000 });
  await expect(page.getByText('tool-second-2')).toBeVisible({ timeout: 2_000 });
  await expect(page.getByText('tool-second-3')).toBeVisible({ timeout: 2_000 });
  await expect(page.getByText('tool-second-5')).toHaveCount(0, { timeout: 100 });
});

test('rapid session switching always shows the correct session thread', async ({ mockedPage: page }) => {
  // Regression: reconcileLoad replaced state wholesale when incoming.sessionId
  // didn't match state.sessionId. A stale doFetch for session A could resolve
  // while the view showed session B, pushing A's session+messages into the
  // reducer. The cache mirror then wrote A's data into B's cache entry. On the
  // next visit to B, getCachedSession(B) returned A's messages, corrupting the
  // thread permanently.
  //
  // Fix: reconcileLoad must return `state` unchanged (drop the incoming data)
  // when session IDs don't match instead of replacing wholesale.
  const SESSION_A = MOCK_SESSION.id;
  const SESSION_B = MOCK_SESSION_2.id;

  const msgA = {
    id: 'rapid-msg-a',
    sessionId: SESSION_A,
    timeCreated: Date.now() - 1000,
    timeUpdated: Date.now() - 1000,
    data: { role: 'assistant', finish: 'end_turn' },
  };
  const partA = {
    id: 'rapid-part-a',
    messageId: msgA.id,
    sessionId: SESSION_A,
    timeCreated: Date.now() - 1000,
    timeUpdated: Date.now() - 1000,
    data: { type: 'text', text: 'Message unique to session A' },
  };

  const sessionA = mockSessionWithLiveConnection();
  const sessionB = { ...MOCK_SESSION_2, liveConnection: false };

  // Session A: delayed so its fetch is still in-flight when the user
  // clicks to B. This makes doFetch_A resolve after B's initial dispatch,
  // triggering the stale-dispatch race.
  await page.route(new RegExp(`/api/session/${SESSION_A}(\\?|$)`), async (route) => {
    await new Promise((r) => setTimeout(r, 400));
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        session: sessionA,
        messages: [msgA],
        parts: [partA],
        totalMessages: 1,
        defaultAgent: 'build',
        defaultModel: '',
      }),
    });
  });

  // Session B: fast, no messages.
  await page.route(new RegExp(`/api/session/${SESSION_B}(\\?|$)`), (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        session: sessionB,
        messages: [],
        parts: [],
        totalMessages: 0,
        defaultAgent: '',
        defaultModel: '',
      }),
    }),
  );

  await page.route(new RegExp(`/api/session/${SESSION_A}/events`), (route) => route.abort());
  await page.route(new RegExp(`/api/session/${SESSION_B}/events`), (route) => route.abort());

  await page.route('/api/sessions*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([sessionA, sessionB]),
    }),
  );

  // Open A and wait for its message (confirms A's fetch completed and cached).
  await page.goto(`/session/${SESSION_A}`);
  await expect(page.getByTestId('session-layout')).toBeVisible();
  await expect(page.locator('.oc-msg-assistant', { hasText: 'Message unique to session A' })).toBeVisible({ timeout: 5_000 });

  // Switch to B — A's next fetch will be delayed 400 ms.
  await page.locator('.session-sidebar-item', { hasText: MOCK_SESSION_2.title }).click();
  await expect(page.getByRole('banner')).toContainText(MOCK_SESSION_2.title, { timeout: 1_000 });

  // Wait for A's delayed fetch to resolve while we're on B. The stale
  // doFetch_A resolves here; without the fix it corrupts B's reducer state
  // and then B's cache via the cache-mirror effect.
  await page.waitForTimeout(600);

  // A's messages must NOT bleed into B's thread.
  await expect(page.locator('.oc-msg-assistant', { hasText: 'Message unique to session A' })).toHaveCount(0);

  // Navigate back to A and then return to B. If B's cache was corrupted
  // by the stale dispatch, this second visit to B shows A's messages.
  await page.locator('.session-sidebar-item', { hasText: MOCK_SESSION.title }).click();
  await expect(page.locator('.oc-msg-assistant', { hasText: 'Message unique to session A' })).toBeVisible({ timeout: 3_000 });
  await page.locator('.session-sidebar-item', { hasText: MOCK_SESSION_2.title }).click();
  await expect(page.getByRole('banner')).toContainText(MOCK_SESSION_2.title, { timeout: 1_000 });
  // This is the final assertion: B must show its empty thread, not A's messages.
  await expect(page.locator('.oc-msg-assistant', { hasText: 'Message unique to session A' })).toHaveCount(0, { timeout: 2_000 });
});

// ---------------------------------------------------------------------------
// Regression: streaming text survives a session-switch round-trip
// ---------------------------------------------------------------------------
//
// User-reported bug: while session A is actively streaming, the user
// switches to session B and back to A. After the round-trip, the
// streamed text shows gaps ("text with missing sections") that only a
// browser refresh restores. The previous backend fix (cache invalidation
// on SSE close, sse.spec.ts:292) handled the *whole-message* variant
// where an entire new message arrives during the gap. This test guards
// the more subtle *partial-text* variant: delta-accumulated text loses
// chunks that were already streamed before the gap.
//
// Root cause: when the user returns to A, the cache restores
// "1 2 3 4 5 " (everything previously streamed via SSE). doFetch
// ('reconcile') refetches the server snapshot which — due to OpenCode's
// DB lagging its SSE stream by hundreds of ms — only has "1 2 3 ".
// `upsertSnapshotPart` runs with an empty `_deltaOwnedFields` map (the
// cache mirror doesn't persist it) and so the snapshot wins fully,
// wiping the cached "4 5 ". New deltas from the reattached EventSource
// then append "6 7 " onto "1 2 3 " → final text "1 2 3 6 7 ", with
// the 4 and 5 chunks gone.
//
// Fix: useSession re-seeds `_deltaOwnedFields` from cached parts on
// revisit, and `upsertSnapshotPart` uses a "longest string wins" rule
// for delta-owned streaming fields (which are append-only on the wire).
test('partial streamed text survives switching away and back', async ({ mockedPage: page }) => {
  const SESSION_A = MOCK_SESSION.id;
  const SESSION_B = MOCK_SESSION_2.id;

  const liveSessionA = mockSessionWithLiveConnection({
    ...MOCK_SESSION,
    status: 'busy',
    liveConnection: true,
  });

  // Track whether the user has navigated away from session A so the
  // mock can serve different bodies on initial-load vs reconcile-after-
  // return. A boolean flag is used instead of a request counter because
  // the useSession hook fires multiple fetches on the initial visit
  // (the initial doFetch plus a reconcile triggered by the SSE
  // onopen handler). A counter-based check (`=== 1`) would let the
  // second fetch serve the reconcile snapshot before the user ever
  // leaves, causing the snapshot's text to collide with in-flight SSE
  // deltas and produce duplicated or missing tokens.
  let hasLeftSessionA = false;

  const MSG_ID = 'stream-msg-a';
  const PART_ID = 'stream-part-a';
  const MSG_CREATED_AT = Date.now() - 5_000;

  // The reconcile snapshot returns DB-lagging text. It must be a
  // strict prefix of what the cache holds — that's the precondition
  // that triggers the bug.
  const RECONCILE_TEXT = '1 2 3 ';

  await page.route(new RegExp(`/api/session/${SESSION_A}(\\?|$)`), (route) => {
    // Before the user navigates away: empty thread — SSE will stream
    // everything. After returning: part exists, text reflects the
    // stale DB state that lags the live SSE stream.
    const isReconcile = hasLeftSessionA;
    const messages = isReconcile
      ? [
          {
            id: MSG_ID,
            sessionId: SESSION_A,
            timeCreated: MSG_CREATED_AT,
            timeUpdated: MSG_CREATED_AT,
            data: { role: 'assistant' as const },
          },
        ]
      : [];
    const parts = isReconcile
      ? [
          {
            id: PART_ID,
            messageId: MSG_ID,
            sessionId: SESSION_A,
            timeCreated: MSG_CREATED_AT,
            timeUpdated: MSG_CREATED_AT + 100,
            data: { type: 'text', text: RECONCILE_TEXT },
          },
        ]
      : [];
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        session: liveSessionA,
        messages,
        parts,
        totalMessages: messages.length,
        defaultAgent: 'build',
        defaultModel: 'anthropic/claude-3-5-sonnet-20241022',
      }),
    });
  });

  // Session B: idle, no messages, no live connection.
  // Fetching B means the user has navigated away from A.
  await page.route(new RegExp(`/api/session/${SESSION_B}(\\?|$)`), (route) => {
    hasLeftSessionA = true;
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        session: MOCK_SESSION_2,
        messages: [],
        parts: [],
        totalMessages: 0,
        defaultAgent: '',
        defaultModel: '',
      }),
    });
  });

  // Session B's SSE endpoint: empty stream, kept open.
  await page.route(new RegExp(`/api/session/${SESSION_B}/events`), (route) =>
    route.fulfill({
      status: 200,
      contentType: 'text/event-stream',
      headers: { 'Cache-Control': 'no-cache', Connection: 'keep-alive' },
      body: '',
    }),
  );

  await page.route('/api/sessions*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([liveSessionA, MOCK_SESSION_2]),
    }),
  );

  // Install a fake EventSource for session A that streams deltas in
  // two phases:
  //   1st connection (visit A): "1 " "2 " "3 " "4 " "5 "
  //   2nd connection (return to A): "6 " "7 "
  // No replay of 4/5 on reconnect — that mirrors OpenCode's behaviour
  // (its /event stream is live-only, no buffering).
  await page.addInitScript(({ sessionAId, msgId, partId }) => {
    type Phase = { deltas: string[]; intervalMs: number };
    const phases: Phase[] = [
      { deltas: ['1 ', '2 ', '3 ', '4 ', '5 '], intervalMs: 60 },
      { deltas: ['6 ', '7 '], intervalMs: 60 },
    ];
    let phaseIndex = 0;

    class GapStreamingEventSource {
      static CONNECTING = 0;
      static OPEN = 1;
      static CLOSED = 2;

      url: string;
      readyState = GapStreamingEventSource.CONNECTING;
      onopen: ((evt: Event) => void) | null = null;
      onerror: ((evt: Event) => void) | null = null;
      onmessage: ((evt: MessageEvent) => void) | null = null;
      listeners: Record<string, Array<(evt: Event) => void>> = {};
      private timer: number | null = null;
      private isATarget: boolean;

      constructor(url: string) {
        this.url = url;
        this.isATarget = url.includes(`/api/session/${sessionAId}/events`);

        queueMicrotask(() => {
          if (this.readyState === GapStreamingEventSource.CLOSED) return;
          this.readyState = GapStreamingEventSource.OPEN;
          this.onopen?.(new Event('open'));
          if (!this.isATarget) return;

          const phase = phases[phaseIndex] ?? null;
          if (!phase) return;
          const isFirstPhase = phaseIndex === 0;

          let i = 0;
          this.timer = window.setInterval(() => {
            if (this.readyState === GapStreamingEventSource.CLOSED) return;

            // Emit message.created on the first delta of phase 0 only.
            // Phase 1 (after returning) builds on the reconciled message
            // that the REST fetch returns; re-creating it would mask
            // the bug by feeding a fresh empty text snapshot.
            if (i === 0 && isFirstPhase) {
              const created = JSON.stringify({
                type: 'message.created',
                properties: {
                  sessionID: sessionAId,
                  info: {
                    id: msgId,
                    sessionID: sessionAId,
                    role: 'assistant',
                    time: { created: Date.now() },
                  },
                  parts: [{ id: partId, type: 'text', text: '' }],
                },
              });
              this.onmessage?.(new MessageEvent('message', { data: created }));
            }

            if (i >= phase.deltas.length) {
              window.clearInterval(this.timer!);
              this.timer = null;
              // Advance to the next phase so the next EventSource
              // construction (after the user returns) streams the
              // continuation.
              phaseIndex += 1;
              return;
            }

            const deltaText = phase.deltas[i];
            i += 1;

            const delta = JSON.stringify({
              type: 'message.part.delta',
              properties: {
                sessionID: sessionAId,
                messageID: msgId,
                partID: partId,
                field: 'text',
                delta: deltaText,
              },
            });
            this.onmessage?.(new MessageEvent('message', { data: delta }));
            if (i >= phase.deltas.length) {
              phaseIndex += 1;
              window.clearInterval(this.timer!);
              this.timer = null;
            }
          }, phase.intervalMs);
        });
      }

      addEventListener(name: string, cb: (evt: Event) => void) {
        (this.listeners[name] ||= []).push(cb);
      }

      close() {
        this.readyState = GapStreamingEventSource.CLOSED;
        if (this.timer !== null) window.clearInterval(this.timer);
      }
    }

    Object.defineProperty(window, 'EventSource', {
      configurable: true,
      writable: true,
      value: GapStreamingEventSource,
    });
  }, { sessionAId: SESSION_A, msgId: MSG_ID, partId: PART_ID });

  // 1. Visit session A and wait until all five numbers from phase 1
  //    have streamed in. The cache mirror records "1 2 3 4 5 ".
  await page.goto(`/session/${SESSION_A}`);
  await expect(page.getByTestId('session-layout')).toBeVisible();
  const bubble = page.locator('.oc-msg-assistant');
  await expect(bubble).toContainText('1 2 3 4 5', { timeout: 5_000 });

  // 2. Switch to session B. A's EventSource closes; A's REST cache
  //    still has the cached "1 2 3 4 5 " content via the cache mirror.
  await page.locator('.session-sidebar-item', { hasText: MOCK_SESSION_2.title }).click();
  await expect(page.getByRole('banner')).toContainText(MOCK_SESSION_2.title, { timeout: 2_000 });

  // 3. Return to session A. The reconcile fetch returns the
  //    DB-lagging "1 2 3 " snapshot. Phase 2 then streams "6 " "7 ".
  await page.locator('.session-sidebar-item', { hasText: MOCK_SESSION.title }).click();
  await expect(page.getByRole('banner')).toContainText(MOCK_SESSION.title, { timeout: 2_000 });

  // 4. Wait for phase 2 to finish ("6 " + "7 " arrived).
  await expect(bubble).toContainText('6 7', { timeout: 5_000 });

  // 5. The full accumulated text must contain every number 1..7 in
  //    order, with no gaps. Without the fix, the snapshot wipes
  //    "4 5 " and the final text is "1 2 3 6 7 ".
  await expect(bubble).toContainText('1 2 3 4 5 6 7', { timeout: 2_000 });
});

// ---------------------------------------------------------------------------
// Queued-message regression tests
// ---------------------------------------------------------------------------

test('user message after a shell command is NOT flagged as queued', async ({ mockedPage: page }) => {
  // Regression: shell commands (`!cmd`) produce an assistant message
  // with no `finish` field. The old queue detection treated this as
  // "unfinished" and incorrectly showed a "Queued" badge on the next
  // user message.
  const sessionId = MOCK_SESSION.id;
  const now = Date.now();

  const messages = [
    {
      id: 'u1', sessionId, timeCreated: now - 3000,
      data: { role: 'user', time: { created: now - 3000 } },
    },
    {
      // Shell-command assistant: no `finish`, no `error`
      id: 'a1-shell', sessionId, timeCreated: now - 2000,
      data: { role: 'assistant', time: { created: now - 2000, completed: now - 1500 } },
    },
    {
      id: 'u2', sessionId, timeCreated: now - 1000,
      data: { role: 'user', time: { created: now - 1000 } },
    },
    {
      id: 'a2', sessionId, timeCreated: now,
      data: { role: 'assistant', finish: 'stop', time: { created: now, completed: now + 500 } },
    },
  ];

  const parts = [
    {
      id: 'p-u1', sessionId, messageId: 'u1', timeCreated: now - 3000,
      data: { type: 'text', text: 'First prompt' },
    },
    {
      // Completed bash tool — synthesized terminal
      id: 'p-shell', sessionId, messageId: 'a1-shell', timeCreated: now - 2000,
      data: { type: 'tool', tool: 'bash', state: { status: 'completed', input: { command: 'ls' }, output: 'file.txt' } },
    },
    {
      id: 'p-u2', sessionId, messageId: 'u2', timeCreated: now - 1000,
      data: { type: 'text', text: 'Second prompt after shell' },
    },
    {
      id: 'p-a2-text', sessionId, messageId: 'a2', timeCreated: now,
      data: { type: 'text', text: 'Here is the reply.' },
    },
  ];

  await page.route(new RegExp(`/api/session/${sessionId}(\\?|$)`), (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        session: { ...MOCK_SESSION, status: 'waiting' },
        messages,
        parts,
        totalMessages: messages.length,
      }),
    }),
  );

  await page.goto(SESSION_URL);
  await expect(page.getByTestId('session-layout')).toBeVisible();

  // Wait for the thread to render all messages.
  await expect(page.locator('.oc-msg-user')).toHaveCount(2, { timeout: 5_000 });

  // The second user message should be visible and NOT have a "Queued" badge.
  const secondUserMsg = page.locator('.oc-msg-user', { hasText: 'Second prompt after shell' });
  await expect(secondUserMsg).toBeVisible({ timeout: 3_000 });
  // The queued badge should NOT be present.
  await expect(secondUserMsg.locator('.oc-msg-queued-badge')).toHaveCount(0);
  // The message element itself should not have the queued class.
  await expect(secondUserMsg).not.toHaveClass(/oc-msg-queued/);
});

test('multiple user messages after shell commands do NOT cascade as queued', async ({ mockedPage: page }) => {
  // Regression: after a shell command, every subsequent user message
  // was incorrectly flagged as queued because the shell-command
  // assistant message has no `finish`.
  const sessionId = MOCK_SESSION.id;
  const now = Date.now();

  const messages = [
    {
      id: 'u1', sessionId, timeCreated: now - 6000,
      data: { role: 'user', time: { created: now - 6000 } },
    },
    {
      // First shell command — no finish
      id: 'a1-shell', sessionId, timeCreated: now - 5000,
      data: { role: 'assistant', time: { created: now - 5000, completed: now - 4500 } },
    },
    {
      id: 'u2', sessionId, timeCreated: now - 4000,
      data: { role: 'user', time: { created: now - 4000 } },
    },
    {
      id: 'a2', sessionId, timeCreated: now - 3000,
      data: { role: 'assistant', finish: 'stop', time: { created: now - 3000, completed: now - 2500 } },
    },
    {
      id: 'u3', sessionId, timeCreated: now - 2000,
      data: { role: 'user', time: { created: now - 2000 } },
    },
    {
      // Second shell command — no finish
      id: 'a3-shell', sessionId, timeCreated: now - 1000,
      data: { role: 'assistant', time: { created: now - 1000, completed: now - 500 } },
    },
    {
      id: 'u4', sessionId, timeCreated: now,
      data: { role: 'user', time: { created: now } },
    },
  ];

  const parts = [
    { id: 'p-u1', sessionId, messageId: 'u1', timeCreated: now - 6000, data: { type: 'text', text: 'Prompt one' } },
    { id: 'p-shell1', sessionId, messageId: 'a1-shell', timeCreated: now - 5000, data: { type: 'tool', tool: 'bash', state: { status: 'completed', input: { command: 'ls' }, output: 'a.txt' } } },
    { id: 'p-u2', sessionId, messageId: 'u2', timeCreated: now - 4000, data: { type: 'text', text: 'Prompt two' } },
    { id: 'p-a2', sessionId, messageId: 'a2', timeCreated: now - 3000, data: { type: 'text', text: 'Reply two' } },
    { id: 'p-u3', sessionId, messageId: 'u3', timeCreated: now - 2000, data: { type: 'text', text: 'Prompt three' } },
    { id: 'p-shell2', sessionId, messageId: 'a3-shell', timeCreated: now - 1000, data: { type: 'tool', tool: 'bash', state: { status: 'completed', input: { command: 'pwd' }, output: '/home' } } },
    { id: 'p-u4', sessionId, messageId: 'u4', timeCreated: now, data: { type: 'text', text: 'Prompt four' } },
  ];

  await page.route(new RegExp(`/api/session/${sessionId}(\\?|$)`), (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        session: { ...MOCK_SESSION, status: 'done' },
        messages,
        parts,
        totalMessages: messages.length,
      }),
    }),
  );

  await page.goto(SESSION_URL);
  await expect(page.getByTestId('session-layout')).toBeVisible();

  // All four user messages should be visible.
  for (const text of ['Prompt one', 'Prompt two', 'Prompt three', 'Prompt four']) {
    await expect(page.locator('.oc-msg-user', { hasText: text })).toBeVisible({ timeout: 3_000 });
  }

  // None of the user messages should have a "Queued" badge.
  const queuedBadges = page.locator('.oc-msg-queued-badge');
  await expect(queuedBadges).toHaveCount(0);
  // No user message should have the queued class.
  const queuedMessages = page.locator('.oc-msg-queued');
  await expect(queuedMessages).toHaveCount(0);
});

test('genuinely queued user message still shows the Queued badge', async ({ mockedPage: page }) => {
  // Sanity check: a user message that follows a genuinely unfinished
  // assistant turn (still streaming, no completed parts) SHOULD show
  // the "Queued" badge.
  const sessionId = MOCK_SESSION.id;
  const now = Date.now();

  const messages = [
    {
      id: 'u1', sessionId, timeCreated: now - 3000,
      data: { role: 'user', time: { created: now - 3000 } },
    },
    {
      // Genuinely unfinished assistant: no finish, tool still running
      id: 'a1-running', sessionId, timeCreated: now - 2000,
      data: { role: 'assistant', time: { created: now - 2000 } },
    },
    {
      id: 'u2-queued', sessionId, timeCreated: now - 1000,
      data: { role: 'user', time: { created: now - 1000 } },
    },
  ];

  const parts = [
    { id: 'p-u1', sessionId, messageId: 'u1', timeCreated: now - 3000, data: { type: 'text', text: 'First prompt' } },
    {
      // Tool still running — NOT a synthesized terminal
      id: 'p-running', sessionId, messageId: 'a1-running', timeCreated: now - 2000,
      data: { type: 'tool', tool: 'bash', state: { status: 'running', input: { command: 'sleep 60' } } },
    },
    { id: 'p-u2', sessionId, messageId: 'u2-queued', timeCreated: now - 1000, data: { type: 'text', text: 'Queued prompt' } },
  ];

  await page.route(new RegExp(`/api/session/${sessionId}(\\?|$)`), (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        session: { ...MOCK_SESSION, status: 'busy' },
        messages,
        parts,
        totalMessages: messages.length,
      }),
    }),
  );

  await page.goto(SESSION_URL);
  await expect(page.getByTestId('session-layout')).toBeVisible();

  // The second user message should be visible and SHOULD have a "Queued" badge.
  const queuedMsg = page.locator('.oc-msg-user', { hasText: 'Queued prompt' });
  await expect(queuedMsg).toBeVisible({ timeout: 3_000 });
  await expect(queuedMsg).toHaveClass(/oc-msg-queued/);
  await expect(queuedMsg.locator('.oc-msg-queued-badge')).toBeVisible();
});
