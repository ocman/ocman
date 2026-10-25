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
import type { OceEvent, OcePrompt } from '../src/lib/conversation/events';
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

export async function installDefaultRoutes(page: Page) {
  // Keep unmocked API calls out of Vite's absent-backend proxy. Specific routes below take priority.
  await page.route('/api/**', (route: Route) =>
    route.fulfill({ status: 404, contentType: 'application/json', body: JSON.stringify({ error: 'not mocked' }) }),
  );

  const zeroUsage = { tokens: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, cost: 0, estCost: 0 };
  await page.route(/\/api\/factory\/epics(\?|$)/, (route) => route.fulfill({ json: [] }));
  await page.route(/\/api\/factory\/epics\/[^/]+\/usage$/, (route) => route.fulfill({ json: {
    total: zeroUsage, phases: { plan: zeroUsage, implement: zeroUsage, verify: zeroUsage, deliver: zeroUsage }, attempts: [], incomplete: false,
  } }));

  // Auth: no auth required (open access)
  await page.route('/api/auth/me', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ authenticated: true, authRequired: false }),
    }),
  );

  // Mounted once authentication succeeds. Without this stub, the Vite proxy
  // can return a 401 and immediately send the app back to the login screen.
  await page.route('/api/mcp/config', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ configured: true }) }),
  );
  await page.route('/api/client-activity', (route: Route) =>
    route.fulfill({ status: 204, body: '' }),
  );
  await page.route('/api/plugins/panes*', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }),
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
        hosts: [
          {
            remoteId: 'local',
            remoteName: 'This machine',
            capabilities: {
              gitDiff: true,
              worktrees: true,
              tmux: true,
              projects: true,
              opencodeLaunch: true,
              opencode: true,
            },
          },
        ],
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
  await page.route(/\/api\/metrics(?:\/performance)?(?:\?.*)?$/, (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        summary: {
          requests: 42,
          completedRequests: 42,
          successfulRequests: 40,
          errorRequests: 2,
          errorRate: 2 / 42,
          totalTokens: 100_000,
          inputTokens: 70_000,
          outputTokens: 30_000,
          avgTokensPerSec: 150,
          avgDurationMs: 5_000,
          p50DurationMs: 4_000,
          p95DurationMs: 9_000,
          totalDurationMs: 210_000,
          cacheHitRate: 0.35,
          cacheReadTokens: 25_000,
          cacheWriteTokens: 10_000,
          totalCost: 1.23,
          totalCalcCost: 1.10,
          totalEffectiveCost: 1.23,
          costPerSuccessfulRequest: 1.23 / 40,
        },
        series: [],
        costByModel: { models: [], series: [] },
        dailyEstimatedCostByModel: { models: [], series: [] },
        dailyEffectiveCostByModel: { models: [], series: [] },
        agents: [],
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
  await page.route('/api/analytics/overview', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ inventoryScope: 'local', totalSessions: 12, subagentSessions: 2, totalProjects: 3, totalRoutines: 1, routineRunsByStatus: { done: 4 }, factoryEpicsByStatus: { active: 1 }, factoryIssuesByStatus: { done: 5 }, factoryAttemptsByPhase: { terminal: 6 }, factoryAttemptsByTerminalOutcome: { successful: 5 } }) }),
  );
  await page.route('/api/permission-stats*', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ eligibleRequests: 2, autoApprovedCount: 1, autoApprovedRate: 0.5, judgmentRequests: 1, manualPreemptions: 0, manualPreemptionRate: 0, medianJudgmentDurationMs: 10, medianManualResponseDurationMs: 20, userDecisionCount: 1, userDecisionRate: 0.5, affectedSessions: 1, unresolvedEligibleRequests: 0, observedUserWaitMs: 5000, p50UserWaitMs: 5000, p95UserWaitMs: 5000, daily: [] }) }),
  );
  await page.route('/api/metric-logs*', (route: Route) => {
    const kind = new URL(route.request().url()).searchParams.get('kind') ?? 'project';
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ kind, total: 0, availableAgents: [], availableModels: [], [`${kind}s`]: [] }),
    });
  });

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
    // Handle sub-paths like /agents, /commands, /models, /stream
    if (url.includes('/stream')) {
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
    if (url.includes('/queue')) {
      // Follow-up message queue (#58) — empty by default.
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) });
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

  // Tmux — not available in CI / test environment
  await page.route('/api/tmux/sessions', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ available: false, sessions: [] }) }),
  );
  await page.route('/api/tmux/clients', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ available: false, clients: [] }) }),
  );

  // System stats (debug/perf panel)
  await page.route('/api/system/stats', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        memory: { alloc: 0, totalAlloc: 0, sys: 0, heapAlloc: 0, heapSys: 0, heapInuse: 0, heapIdle: 0, heapReleased: 0 },
        gc: { numGC: 0, lastGC: 0, pauseNs: 0 },
        goroutines: 1,
        uptime: 0,
      }),
    }),
  );

  // Git info (branch/dirty status shown on session/project cards)
  await page.route('/api/git/info*', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ branch: 'main', ahead: 0, behind: 0, dirty: false }),
    }),
  );

  // Settings endpoints (loaded on mount of the Settings tab). When a
  // real ocman backend is running on :8229 with auth enabled, the
  // Vite proxy will forward unmocked /api/settings/* requests and the
  // backend returns 401 — which flips the auth store to unauthenticated
  // and unmounts whatever page the test is on. Stubbing these here
  // keeps tests deterministic regardless of host environment.
  //
  // Catch-all first (lowest priority): any settings endpoint not
  // explicitly stubbed below returns an empty object so it can never
  // escape to the proxy and 401 the auth store. Specific stubs
  // registered afterwards take priority (later page.route wins).
  await page.route('/api/project/settings?*', (route: Route) =>
    route.fulfill({ json: { models: [], off: false, defaultAgent: 'build' } }),
  );
  await page.route('/api/settings/**', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({}) }),
  );
  await page.route('/api/settings/default-agent', (route: Route) =>
    route.fulfill({ json: { defaultAgent: 'build' } }),
  );
  // Every assistant text block loads the custom link rules.
  await page.route('/api/settings/link-preview-rules', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ rules: [] }) }),
  );
  // Remotes settings group mounts RemoteSettings, which fetches
  // /api/settings/remote-access and /api/remotes on mount. Both must be
  // stubbed: an unmocked 401 (from a real auth-enabled backend behind the
  // vite proxy) trips the global AuthError handler and boots the test to
  // the lockscreen before the component's own .catch() can swallow it.
  await page.route('/api/settings/remote-access', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ enabled: false, instanceId: '', listenAddr: '' }),
    }),
  );
  await page.route('/api/remotes', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) }),
  );
  await page.route('/api/settings/prompt-sections', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) }),
  );
  await page.route('/api/settings/judge-delay', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ delayMs: 0 }) }),
  );
  await page.route('/api/settings/prompt-templates', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({}) }),
  );
  // Sharing settings + global share list (SharingSettings mounts on the
  // Settings tab and fetches both on mount). Unmocked → proxied to the
  // dead backend → 401 flips auth to unauthenticated and unmounts the
  // page, which breaks the Account/Sign-out settings tests.
  await page.route('/api/settings/sharing', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ enabled: true }) }),
  );
  await page.route('/api/shares', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) }),
  );
  // Remote-access + remotes list (RemoteSettings mounts in the Settings
  // "Remotes" group and fetches both on mount). Same failure mode as
  // above: unmocked → 401 → auth flips → settings page unmounts,
  // detaching the Account/Sign-out controls mid-test.
  await page.route('/api/settings/remote-access', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ instanceId: '', listening: false, listenAddr: '', tls: false }),
    }),
  );
  await page.route('/api/remotes', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) }),
  );

  // App-wide SSE stream (useGlobalEvents) — mounted at the app root on
  // EVERY page. Without a stub it proxies to the (absent) Go backend on
  // :8229; the EventSource then auto-reconnects on the connection error,
  // producing a steady reconnect storm against the `vite preview` proxy
  // for the whole suite. Under the slower CI runner that accumulated load
  // eventually starves the preview server and later navigations fail with
  // ERR_CONNECTION_REFUSED — the root cause of the flaky e2e job. Fulfil
  // it with an empty, already-complete event stream so the EventSource
  // opens cleanly and never reconnects.
  await page.route('/api/events', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'text/event-stream', body: '' }),
  );

  // Upstream forge remotes (PR/issue sidebar). Unmocked → proxied to the
  // dead backend; stub as "no upstreams" so the pane stays hidden.
  await page.route('/api/project/upstreams*', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ upstreams: [] }) }),
  );

  // In-app terminal windows (TerminalPane). Unmocked GETs proxy to the
  // dead backend; stub as "no windows".
  await page.route('/api/term/windows*', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ windows: [] }) }),
  );

  // Link previews resolve for any message with a link: stub as "nothing".
  await page.route('/api/previews/providers*', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ providers: [] }) }),
  );
  await page.route('/api/previews/resolve*', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ previews: [] }) }),
  );
}

// ---------------------------------------------------------------------------
// Conversation stream helpers (GET /api/session/{id}/stream)
// ---------------------------------------------------------------------------

/** Stream epoch used by every mocked conversation stream. */
export const OCE_EPOCH = 'e2e';

const DURABLE_EVENTS = new Set([
  'message.upserted', 'message.removed', 'part.upserted', 'part.removed', 'session.updated',
  'history.rewritten', 'history.committed', 'child.upserted', 'session.error',
]);

/**
 * One SSE frame as the server writes it (design §4.1): `event: oce`,
 * `id: <epoch>:<seq>` and the envelope as `data`. stream.reset and
 * stream.error carry no id line.
 */
export function oceFrame(sessionId: string, seq: number, event: OceEvent): string {
  const durability = event.type === 'part.delta' ? 'ephemeral' : DURABLE_EVENTS.has(event.type) ? 'durable' : 'live';
  const envelope = { v: 1, epoch: OCE_EPOCH, seq, sessionId, platform: 'opencode', at: Date.now(), durability, event };
  const id = event.type === 'stream.reset' || event.type === 'stream.error' ? '' : `id: ${OCE_EPOCH}:${seq}\n`;
  return `event: oce\n${id}data: ${JSON.stringify(envelope)}\n\n`;
}

/** The first frame of every connection; a cold connect is not resumed. */
export function oceHello(resumed = false): OceEvent {
  return { type: 'stream.hello', protocol: 1, resumed, revision: 0 };
}

/**
 * Install a conversation stream stub for a session. The first connection
 * receives `stream.hello` followed by `events` (seq 1..n). Playwright
 * closes fulfilled responses, so the client reconnects with `?after=`; a
 * reconnect gets a resumed hello and no replay, like a server whose log
 * holds nothing newer.
 *
 * Usage:
 *   await mockStream(page, 'sess-abc123', [
 *     { type: 'session.status', status: 'busy' },
 *   ]);
 */
export async function mockStream(
  page: Page,
  sessionId: string,
  events: OceEvent[],
): Promise<void> {
  let delivered = false;
  await page.route(
    new RegExp(`/api/session/${sessionId}/stream`),
    async (route) => {
      const resumed = new URL(route.request().url()).searchParams.has('after');
      const frames = delivered
        ? [oceFrame(sessionId, events.length, oceHello(resumed))]
        : [oceFrame(sessionId, 0, oceHello(false)), ...events.map((event, i) => oceFrame(sessionId, i + 1, event))];
      delivered = true;
      await route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        headers: {
          'Cache-Control': 'no-cache',
          Connection: 'keep-alive',
        },
        body: frames.join(''),
      });
    },
  );
}

/**
 * A message with one text part, as the stream announces it: a
 * `message.upserted` followed by its `part.upserted`. The part is final
 * once the message has finished (user messages always are).
 */
export function oceMessage(opts: {
  sessionId: string;
  role: 'user' | 'assistant';
  text: string;
  finish?: string;
  msgId?: string;
}): { events: OceEvent[]; msgId: string; partId: string } {
  const msgId = opts.msgId ?? `msg-${Math.random().toString(36).slice(2)}`;
  const partId = `part-${msgId}`;
  const created = Date.now();
  const finish = opts.finish ?? (opts.role === 'assistant' ? 'end_turn' : undefined);
  const events: OceEvent[] = [
    {
      type: 'message.upserted',
      message: {
        id: msgId,
        sessionId: opts.sessionId,
        timeCreated: created,
        data: { role: opts.role, ...(finish ? { finish } : {}), time: { created } },
      },
    },
    {
      type: 'part.upserted',
      part: { id: partId, messageId: msgId, sessionId: opts.sessionId, data: { type: 'text', text: opts.text } },
      final: opts.role === 'user' || !!finish,
    },
  ];
  return { events, msgId, partId };
}

/** A pending permission or question prompt asked by the session itself. */
export function promptAsked(sessionId: string, prompt: Omit<OcePrompt, 'sessionId' | 'askedAt'>): OceEvent {
  return { type: 'prompt.asked', prompt: { sessionId, askedAt: Date.now(), ...prompt } };
}

// ---------------------------------------------------------------------------
// Capabilities helper — returns a `capabilities` response with `portAvailable`
// set so the composer is enabled.
// ---------------------------------------------------------------------------

export function makeCapabilitiesWithPort() {
  return {
    worktreeSessions: true,
    hosts: [
      {
        remoteId: 'local',
        remoteName: 'This machine',
        capabilities: {
          gitDiff: true,
          worktrees: true,
          tmux: true,
          projects: true,
          opencodeLaunch: true,
          opencode: true,
        },
      },
    ],
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
