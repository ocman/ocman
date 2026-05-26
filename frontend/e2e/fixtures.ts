/**
 * Shared Playwright fixtures and helpers for the ocman e2e suite.
 *
 * All tests import `test` and `expect` from this file rather than
 * directly from `@playwright/test` so that the shared mock-backend
 * route interceptors are always active.
 *
 * Mock strategy:
 *   - /api/auth/me  → 200 { authenticated: false, authRequired: false }
 *     (no auth wall by default; individual auth tests override this)
 *   - /api/sessions → 200 with a small synthetic session list
 *   - /api/projects → 200 with synthetic project list
 *   - /api/capabilities → 200 with standard capability flags
 *   - All other /api/* routes → 200 with sensible empty/zero stubs
 *
 * Tests that need a specific API shape call `page.route(...)` after
 * importing the page fixture — later `page.route` registrations take
 * priority over earlier ones in Playwright.
 */

import { test as base, expect, type Page, type Route } from '@playwright/test';
export { expect };
export type { Page };

// ---------------------------------------------------------------------------
// Synthetic data
// ---------------------------------------------------------------------------

export const MOCK_SESSION = {
  id: 'sess-abc123',
  platform: 'opencode',
  title: 'Fix the login bug',
  directory: '/home/user/projects/myapp',
  status: 'waiting',
  messageCount: 12,
  durationMs: 3_600_000,
  timeCreated: Date.now() - 3_600_000,
  timeUpdated: Date.now() - 60_000,
  seen: false,
  archived: false,
  pinned: false,
  pinnedAt: 0,
  liveConnection: false,
  pendingPermission: false,
  pendingQuestion: false,
};

export const MOCK_SESSION_2 = {
  ...MOCK_SESSION,
  id: 'sess-def456',
  title: 'Refactor auth module',
  status: 'idle',
  timeCreated: Date.now() - 7_200_000,
  timeUpdated: Date.now() - 120_000,
};

export const MOCK_PROJECT = {
  directory: '/home/user/projects/myapp',
  sessionCount: 2,
  messageCount: 24,
  totalTokensIn: 10_000,
  totalTokensOut: 5_000,
  lastUsed: Date.now() - 60_000,
};

// ---------------------------------------------------------------------------
// Default API stubs — applied to every test page
// ---------------------------------------------------------------------------

async function installDefaultRoutes(page: Page) {
  // Auth: no auth required (open access)
  await page.route('/api/auth/me', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ authenticated: true, authRequired: false }),
    }),
  );

  // Sessions list
  await page.route('/api/sessions*', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([MOCK_SESSION, MOCK_SESSION_2]),
    }),
  );

  // Projects list
  await page.route('/api/projects*', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([MOCK_PROJECT]),
    }),
  );

  // Capabilities — must match PlatformCapabilityEntry / CapabilitiesResponse types:
  // { id, displayName, available, capabilities: PlatformCapabilities }
  await page.route('/api/capabilities', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        worktreeSessions: true,
        platforms: [
          {
            id: 'opencode',
            displayName: 'OpenCode',
            available: true,
            capabilities: {
              composer: true,
              respondPermission: true,
              respondQuestion: true,
              abort: true,
              compact: true,
              events: true,
              agentCatalog: true,
              modelCatalog: true,
              slashCommands: true,
              liveConnectionHint: '',
              autoApprove: false,
              shellExec: false,
              fileChanges: false,
              sessionInfo: false,
            },
          },
        ],
      }),
    }),
  );

  // Metrics (Stats tab)
  await page.route('/api/metrics*', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        summary: {
          requests: 42,
          totalTokens: 100_000,
          inputTokens: 70_000,
          outputTokens: 30_000,
          avgTokensPerSec: 150,
          avgDurationMs: 5_000,
          totalDurationMs: 210_000,
          cacheHitRate: 0.35,
          cacheReadTokens: 25_000,
          cacheWriteTokens: 10_000,
          totalCost: 1.23,
          totalCalcCost: 1.10,
        },
        series: [],
        stopReasons: [{ reason: 'end_turn', count: 40 }, { reason: 'error', count: 2 }],
        requests: [],
        totalRequests: 0,
        sessions: [],
        totalSessions: 0,
        projects: [],
        totalProjects: 0,
        availableAgents: [],
        availableModels: [],
      }),
    }),
  );

  // Activity / usage endpoints
  await page.route('/api/activity*', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) }),
  );
  await page.route('/api/models*', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) }),
  );
  await page.route('/api/hourly*', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) }),
  );
  await page.route('/api/hourly-tokens*', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) }),
  );

  // Session detail — matches /api/session/<id> and /api/session/<id>/sub-path
  await page.route('/api/session/**', (route) => {
    const url = route.request().url();
    // Handle sub-paths like /agents, /commands, /models, /events
    if (url.includes('/events')) {
      // SSE — just close with empty stream
      return route.fulfill({ status: 200, contentType: 'text/event-stream', body: '' });
    }
    if (url.includes('/agents')) {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) });
    }
    if (url.includes('/commands')) {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) });
    }
    if (url.includes('/models')) {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) });
    }
    if (url.includes('/permissions')) {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) });
    }
    if (url.includes('/questions')) {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) });
    }
    if (url.includes('/auto-approve')) {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ enabled: false, overridden: false }) });
    }
    // Full session detail — must match the SessionDetail interface:
    // { session: Session, messages: Message[], parts: Part[], totalMessages?, defaultAgent?, defaultModel? }
    // Determine which mock session to return based on the session ID in the URL.
    const session = url.includes(MOCK_SESSION_2.id) ? MOCK_SESSION_2 : MOCK_SESSION;
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        session,
        messages: [],
        parts: [],
        totalMessages: 0,
        defaultAgent: '',
        defaultModel: '',
      }),
    });
  });

  // Suppress debug/remote-log noise
  await page.route('/api/debug/**', (route: Route) =>
    route.fulfill({ status: 204, body: '' }),
  );

  // Sessions notify (favicon)
  await page.route('/api/sessions/notify*', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) }),
  );

  // Whisper
  await page.route('/api/whisper/status', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ available: false }) }),
  );

  // Tmux — not available in CI / test environment
  await page.route('/api/tmux/sessions', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ available: false, sessions: [] }) }),
  );
  await page.route('/api/tmux/clients', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ available: false, clients: [] }) }),
  );
}

// ---------------------------------------------------------------------------
// SSE event helpers
// ---------------------------------------------------------------------------

/**
 * Build a raw SSE payload string from a top-level event object.
 *
 * The format mirrors what the ocman backend emits for OpenCode events:
 *   { "type": "...", "properties": { ...event-specific fields } }
 *
 * Pass `properties` nested under the event, OR pass the payload flat and
 * the helper will wrap it. The `type` field is always at the top level.
 */
export function sseEvent(payload: { type: string; properties?: Record<string, unknown>; [key: string]: unknown }): string {
  const { type, properties, ...rest } = payload;
  // If properties is already provided, use it as-is. Otherwise wrap the
  // remaining fields (everything except `type`) in `properties`.
  const data = JSON.stringify({
    type,
    properties: properties ?? rest,
  });
  return `data: ${data}\n\n`;
}

/**
 * Install an SSE stub for a specific session that delivers the provided
 * events immediately when the EventSource connects, then keeps the
 * connection open (sends no further data).
 *
 * The first SSE connection receives all events. Any subsequent reconnect
 * attempts are aborted so the browser's EventSource does not fire `onopen`
 * again. Without this guard, useSession's `onopen` handler calls
 * doFetch('reconcile') on reconnect, which calls viewFromDetail — always
 * setting pendingPermission/pendingQuestion to null — wiping any in-memory
 * prompt state that was set by the initial SSE events.
 *
 * Usage:
 *   await mockSse(page, 'sess-abc123', [
 *     sseEvent({ type: 'session.status', properties: { status: 'busy' } }),
 *   ]);
 */
export async function mockSse(
  page: Page,
  sessionId: string,
  events: string[],
): Promise<void> {
  let hasServed = false;
  await page.route(
    new RegExp(`/api/session/${sessionId}/events`),
    async (route) => {
      if (hasServed) {
        // Abort reconnect attempts to prevent useSession from running
        // doFetch('reconcile'), which would clear SSE-derived prompt state.
        await route.abort();
        return;
      }
      hasServed = true;
      await route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        headers: {
          'Cache-Control': 'no-cache',
          Connection: 'keep-alive',
        },
        body: events.join(''),
      });
    },
  );
}

/**
 * Build a mock `message.created` SSE event matching the format that
 * ocman's SSE handler expects:
 *
 *   { type: 'message.created', properties: { info: {...}, parts: [...] } }
 *
 * `extractMessageFromEvent` reads `properties.info` when `properties` exists.
 */
export function sseMessage(opts: {
  sessionId: string;
  role: 'user' | 'assistant';
  text: string;
  finish?: string;
  msgId?: string;
}): { event: string; msgId: string; partId: string } {
  const msgId = opts.msgId ?? `msg-${Math.random().toString(36).slice(2)}`;
  const partId = `part-${msgId}`;
  const event = sseEvent({
    type: 'message.created',
    properties: {
      sessionID: opts.sessionId,
      info: {
        id: msgId,
        sessionID: opts.sessionId,
        role: opts.role,
        finish: opts.finish ?? (opts.role === 'assistant' ? 'end_turn' : undefined),
        time: { created: Date.now() },
      },
      parts: [
        {
          id: partId,
          type: 'text',
          text: opts.text,
        },
      ],
    },
  });
  return { event, msgId, partId };
}

// ---------------------------------------------------------------------------
// Capabilities helper — returns a `capabilities` response with `portAvailable`
// set so the composer is enabled.
// ---------------------------------------------------------------------------

export function makeCapabilitiesWithPort() {
  return {
    worktreeSessions: true,
    platforms: [
      {
        id: 'opencode',
        displayName: 'OpenCode',
        available: true,
        capabilities: {
          composer: true,
          respondPermission: true,
          respondQuestion: true,
          abort: true,
          compact: true,
          events: true,
          agentCatalog: true,
          modelCatalog: true,
          slashCommands: true,
          liveConnectionHint: '',
          autoApprove: false,
          shellExec: false,
          fileChanges: false,
          sessionInfo: false,
        },
      },
    ],
  };
}

/**
 * Return a session detail object with `liveConnection: true` so the
 * composer is enabled (portAvailable = true in the frontend).
 */
export function mockSessionWithLiveConnection(base = MOCK_SESSION) {
  return { ...base, liveConnection: true };
}

// ---------------------------------------------------------------------------
// Fixture definition
// ---------------------------------------------------------------------------

type OcmanFixtures = {
  /** Page with all default API mocks pre-installed. */
  mockedPage: Page;
};

export const test = base.extend<OcmanFixtures>({
  mockedPage: async ({ page }, use) => {
    await installDefaultRoutes(page);
    // Playwright's fixture `use()` is not React's `use`, but the rule's
    // heuristic matches on the identifier. Disable the check here.
    // eslint-disable-next-line react-hooks/rules-of-hooks
    await use(page);
  },
});
