// @vitest-environment jsdom
//
// Test harness for SessionDetail integration tests. Renders the page
// inside a MemoryRouter, replaces useApiStore's actions with vi
// spies, stubs api.* module-level functions, replaces EventSource,
// and short-circuits the hooks that talk to the network outside the
// store (useCapabilities, useTmux, useGitInfo). The harness keeps a
// reference to the most recently constructed FakeEventSource so
// tests can dispatch SSE events.
//
// Module mocks are installed once at module-load via `vi.mock`
// (auto-hoisted by vitest). Each test then mutates the shared
// `mockState` to control the per-test return values; this avoids
// the cost of `vi.resetModules()` + re-importing the entire React
// tree before every test, which on CI ran each mount past the
// async-util timeout.

import { vi } from 'vitest';
import { render, type RenderResult } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useParams } from 'react-router-dom';
import type {
  Session,
  SessionDetail as SessionDetailPayload,
  AgentInfo,
  PlatformCapabilities,
} from '../../../lib/api';

/**
 * Minimal FakeEventSource that records every constructed instance so
 * tests can drive `addEventListener` callbacks. Only the bits the
 * page actually uses are implemented.
 */
export class FakeEventSource {
  static OPEN = 1 as const;
  static CONNECTING = 0 as const;
  static CLOSED = 2 as const;
  static instances: FakeEventSource[] = [];

  url: string;
  readyState: number = FakeEventSource.CONNECTING;
  withCredentials = false;
  onopen: ((ev: Event) => unknown) | null = null;
  onmessage: ((ev: MessageEvent) => unknown) | null = null;
  onerror: ((ev: Event) => unknown) | null = null;

  private listeners = new Map<string, Set<(ev: MessageEvent) => unknown>>();
  closed = false;

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: (ev: MessageEvent) => unknown) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type)!.add(listener);
  }

  removeEventListener(type: string, listener: (ev: MessageEvent) => unknown) {
    this.listeners.get(type)?.delete(listener);
  }

  /** Simulate the upstream opening the stream. */
  open() {
    this.readyState = FakeEventSource.OPEN;
    this.onopen?.(new Event('open'));
  }

  /** Dispatch a typed SSE event (matches addEventListener('foo', …)). */
  emit(type: string, data: unknown) {
    const payload = typeof data === 'string' ? data : JSON.stringify(data);
    const ev = new MessageEvent(type, { data: payload });
    this.listeners.get(type)?.forEach((l) => l(ev));
  }

  /** Dispatch a generic onmessage event. */
  emitMessage(data: unknown) {
    const payload = typeof data === 'string' ? data : JSON.stringify(data);
    const ev = new MessageEvent('message', { data: payload });
    this.onmessage?.(ev);
  }

  close() {
    this.closed = true;
    this.readyState = FakeEventSource.CLOSED;
  }

  static latest(): FakeEventSource | undefined {
    return FakeEventSource.instances[FakeEventSource.instances.length - 1];
  }

  static reset() {
    FakeEventSource.instances.length = 0;
  }
}

// Install fake EventSource + ResizeObserver on globalThis so the
// module mocks below (and the real component code) see them when
// the page first imports. These globals are set in module scope so
// they're in place before any vi.mock factory runs.
(globalThis as unknown as { EventSource: typeof FakeEventSource }).EventSource = FakeEventSource;
if (typeof (globalThis as unknown as { ResizeObserver?: unknown }).ResizeObserver === 'undefined') {
  class StubResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  (globalThis as unknown as { ResizeObserver: typeof StubResizeObserver }).ResizeObserver = StubResizeObserver;
}

/** Default capabilities — full live OpenCode adapter. */
export function fullCaps(): PlatformCapabilities {
  return {
    composer: true,
    respondPermission: true,
    respondQuestion: true,
    abort: true,
    compact: true,
    events: true,
    agentCatalog: true,
    modelCatalog: true,
    slashCommands: true,
    shellExec: true,
    fileChanges: true,
    sessionInfo: true,
    liveConnectionHint: '',
  };
}

/**
 * Per-test mutable state read by the module-scoped mocks. Tests
 * adjust these via `renderSessionPage(opts)`; the mock factories
 * read them at call time so behaviour can change between tests
 * without re-importing the page.
 */
const mockState: {
  caps: PlatformCapabilities;
  apiStub: ReturnType<typeof makeApiStub>;
} = {
  caps: fullCaps(),
  apiStub: makeApiStub(),
};

/**
 * Stub the entire `api` module surface used by SessionDetail. Tests
 * can override individual fields by spreading their own implementations
 * over the result.
 */
export function makeApiStub() {
  return {
    capabilities: vi.fn().mockResolvedValue({ platforms: [] }),
    sessionModels: vi.fn().mockResolvedValue({
      sessionDefault: undefined,
      providerDefaults: {},
      hasProviders: false,
      models: [],
    }),
    agents: vi.fn().mockResolvedValue([] as AgentInfo[]),
    addFavorite: vi.fn().mockResolvedValue(undefined),
    removeFavorite: vi.fn().mockResolvedValue(undefined),
    compactSession: vi.fn().mockResolvedValue(undefined),
    renameSession: vi.fn().mockResolvedValue(undefined),
    executeCommand: vi.fn().mockResolvedValue(undefined),
    runShell: vi.fn().mockResolvedValue(undefined),
  };
}

// ---------------------------------------------------------------------------
// Module mocks. Vitest auto-hoists `vi.mock` calls to the top of the
// module, so these run before any of our imports — including the
// imports inside the SUT — even though they appear here textually.
// Each factory reads from `mockState` at call time so per-test
// changes take effect without re-importing.
// ---------------------------------------------------------------------------

vi.mock('../../../lib/api', async () => {
  const real = await vi.importActual<typeof import('../../../lib/api')>('../../../lib/api');
  return {
    ...real,
    api: new Proxy({} as typeof real.api, {
      get(_target, prop: string) {
        const stub = mockState.apiStub as unknown as Record<string, unknown>;
        if (prop in stub) return stub[prop];
        return (real.api as unknown as Record<string, unknown>)[prop];
      },
    }),
  };
});

vi.mock('../../../lib/useCapabilities', () => ({
  useCapabilities: () => ({
    platforms: [{
      id: 'opencode',
      displayName: 'OpenCode',
      available: true,
      capabilities: mockState.caps,
    }],
  }),
  usePlatformCapabilities: () => mockState.caps,
  useMultiPlatform: () => false,
  useWorktreeSessions: () => false,
}));

vi.mock('../../../lib/useTmux', () => ({
  useTmux: () => ({
    available: false,
    isLocal: false,
    sessions: [],
    clients: [],
    switchSession: vi.fn().mockResolvedValue(undefined),
    findSession: () => undefined,
    launchOpencode: vi.fn().mockResolvedValue({ session: '' }),
  }),
}));

vi.mock('../../../lib/useGitInfo', () => ({
  useGitInfo: () => ({ infos: {}, loading: false, error: null }),
}));

vi.mock('../../../lib/useSessionChanges', () => ({
  useSessionChanges: () => ({
    data: { sessionId: '', supported: false, totalAdditions: 0, totalDeletions: 0, filesChanged: 0, files: [] },
    loading: false,
    error: null,
    refresh: vi.fn(),
  }),
}));

vi.mock('../../../lib/useSessionInfo', () => ({
  useSessionInfo: () => ({
    data: null,
    loading: false,
    error: null,
    refresh: vi.fn(),
  }),
}));

vi.mock('../../../lib/useWorkingTreeDiff', () => ({
  useWorkingTreeDiff: () => ({
    data: { repo: '', branch: '', ahead: 0, behind: 0, files: [], truncated: false },
    loading: false,
    error: null,
    notRepo: false,
    refresh: vi.fn(),
  }),
}));

vi.mock('../../../lib/useFaviconNotify', () => ({
  recheckFaviconNotify: vi.fn(),
}));

vi.mock('../../../lib/useToastNotify', () => ({
  notifyPromptDismissed: vi.fn(),
}));

// Eagerly import the page + apiStore once. Subsequent test calls
// reuse the cached modules instead of paying re-import cost.
import { SessionDetail } from '../SessionDetail';
import { useApiStore } from '../../../lib/apiStore';

/**
 * Adapter that reads the URL :id and forwards it as a prop to the
 * inner SessionDetail. Mirrors the production wrapper in
 * `../index.tsx` so tests exercise the inner component the same way
 * the app does. The eslint disable is for `react-refresh/only-export-
 * components`, which doesn't apply to test harness files anyway —
 * Vite's HMR never touches __tests__/.
 */
// eslint-disable-next-line react-refresh/only-export-components
function SessionDetailRouteAdapter() {
  const { id } = useParams<{ id: string }>();
  return <SessionDetail id={id} />;
}

/** Build a Session fixture with sensible defaults. */
export function makeSession(overrides: Partial<Session> = {}): Session {
  const now = Date.now();
  return {
    id: 'sess_1',
    platform: 'opencode',
    projectId: 'proj_1',
    title: 'Test session',
    directory: '/tmp/proj',
    timeCreated: now - 60_000,
    timeUpdated: now,
    summaryAdditions: 0,
    summaryDeletions: 0,
    summaryFiles: 0,
    shareUrl: null,
    messageCount: 0,
    durationMs: 0,
    totalInputTokens: 0,
    totalOutputTokens: 0,
    totalCost: 0,
    status: 'done',
    liveConnection: true,
    pendingPermission: false,
    pendingQuestion: false,
    archived: false,
    seen: true,
    pinned: false,
    pinnedAt: 0,
    ...overrides,
  };
}

/** Build a SessionDetail fixture. */
export function makeSessionDetail(
  sess: Session,
  overrides: Partial<SessionDetailPayload> = {},
): SessionDetailPayload {
  return {
    session: sess,
    messages: [],
    parts: [],
    totalMessages: 0,
    contextTokenCount: 0,
    defaultAgent: 'build',
    defaultModel: 'anthropic/claude-opus-4',
    ...overrides,
  };
}

export interface RenderOptions {
  sessionId?: string;
  detail?: SessionDetailPayload;
  sessions?: Session[];
  caps?: PlatformCapabilities;
  /** Override apiStore actions individually. */
  storeOverrides?: Record<string, unknown>;
}

export interface RenderHandle {
  result: RenderResult;
  store: {
    getSession: ReturnType<typeof vi.fn>;
    getSessions: ReturnType<typeof vi.fn>;
    sendMessage: ReturnType<typeof vi.fn>;
    listPermissions: ReturnType<typeof vi.fn>;
    respondPermission: ReturnType<typeof vi.fn>;
    listQuestions: ReturnType<typeof vi.fn>;
    respondQuestion: ReturnType<typeof vi.fn>;
    rejectQuestion: ReturnType<typeof vi.fn>;
    abortSession: ReturnType<typeof vi.fn>;
    archiveSession: ReturnType<typeof vi.fn>;
    pinSession: ReturnType<typeof vi.fn>;
    markSessionSeen: ReturnType<typeof vi.fn>;
    setCachedSession: ReturnType<typeof vi.fn>;
    getCachedSession: ReturnType<typeof vi.fn>;
    updateCachedSession: ReturnType<typeof vi.fn>;
    refreshCachedSessions: ReturnType<typeof vi.fn>;
  };
  api: ReturnType<typeof makeApiStub>;
  /** Populated after the page mounts — undefined if SSE never started. */
  sse: () => FakeEventSource | undefined;
}

/**
 * Render the page with all dependencies stubbed. Cheap to call —
 * the SUT is imported once at module load and reused across tests;
 * per-test customisation flows through `mockState` and the
 * `useApiStore.setState` patch.
 */
export function renderSessionPage(opts: RenderOptions = {}): RenderHandle {
  // Reset any FakeEventSource instances from the previous test so
  // `sse()` only returns this run's stream.
  FakeEventSource.reset();

  // Refresh the per-test mock state. The caps object is read
  // lazily by usePlatformCapabilities; the api stub is read via the
  // proxy installed in vi.mock('../../../lib/api') above.
  mockState.caps = opts.caps ?? fullCaps();
  mockState.apiStub = makeApiStub();

  const detail =
    opts.detail ?? makeSessionDetail(makeSession({ id: opts.sessionId ?? 'sess_1' }));
  const storeSpies: RenderHandle['store'] = {
    getSession: vi.fn().mockResolvedValue(detail),
    getSessions: vi.fn().mockResolvedValue(opts.sessions ?? [detail.session]),
    sendMessage: vi.fn().mockResolvedValue(undefined),
    listPermissions: vi.fn().mockResolvedValue([]),
    respondPermission: vi.fn().mockResolvedValue(undefined),
    listQuestions: vi.fn().mockResolvedValue([]),
    respondQuestion: vi.fn().mockResolvedValue(undefined),
    rejectQuestion: vi.fn().mockResolvedValue(undefined),
    abortSession: vi.fn().mockResolvedValue(undefined),
    archiveSession: vi.fn().mockResolvedValue({ ok: true }),
    pinSession: vi.fn().mockResolvedValue({ ok: true }),
    markSessionSeen: vi.fn().mockResolvedValue({ ok: true }),
    setCachedSession: vi.fn(),
    getCachedSession: vi.fn().mockReturnValue(null),
    // updateCachedSession is the cache mirror in SessionDetail's
    // mirror effect: it merges live messages/parts into a cached
    // detail entry. Tests can read its calls to verify the merge.
    updateCachedSession: vi.fn(),
    refreshCachedSessions: vi.fn().mockResolvedValue([]),
  };

  // Apply spies to the real store. Zustand merges the patch onto
  // the existing slice so untouched selectors keep working.
  useApiStore.setState({
    ...storeSpies,
    ...(opts.storeOverrides ?? {}),
  } as unknown as Parameters<typeof useApiStore.setState>[0]);

  const sessionId = opts.sessionId ?? detail.session.id;
  // Mirror what `pages/session-detail/index.tsx` does in production:
  // read the :id from useParams and pass it to the inner component
  // as a prop. Tests target the inner component directly so they
  // exercise its behaviour without the param-propagation indirection.
  const result = render(
    <MemoryRouter initialEntries={[`/session/${sessionId}`]}>
      <Routes>
        <Route path="/session/:id" element={<SessionDetailRouteAdapter />} />
      </Routes>
    </MemoryRouter>,
  );

  return {
    result,
    store: storeSpies,
    api: mockState.apiStub,
    sse: () => FakeEventSource.latest(),
  };
}

/** Convenience: yield to the microtask queue so promises settle. */
export async function flushPromises(times = 4) {
  for (let i = 0; i < times; i++) await Promise.resolve();
}
