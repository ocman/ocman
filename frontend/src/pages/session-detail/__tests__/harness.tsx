// @vitest-environment jsdom
//
// Test harness for SessionDetail integration tests. Renders the page
// inside a MemoryRouter, replaces useApiStore's actions with vi
// spies, stubs api.* module-level functions, replaces EventSource,
// and short-circuits the hooks that talk to the network outside the
// store (useCapabilities, useTmux, useGitInfo). The harness keeps a
// reference to the most recently constructed FakeEventSource so
// tests can dispatch SSE events.

import { vi } from 'vitest';
import { render, type RenderResult } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import type {
  Session,
  SessionDetail as SessionDetailPayload,
  AgentInfo,
  PlatformCapabilities,
  WorkingTreeDiff,
  TmuxSession,
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
  /** Override window.location for git-info / fetch checks. */
}

/**
 * Render the SessionDetail page with all external dependencies
 * stubbed. Returns:
 *   - the @testing-library/react RenderResult
 *   - a `store` proxy giving direct access to vi spies for sessionDetail's
 *     apiStore actions
 *   - the FakeEventSource (after the page mounts the first time)
 *
 * Callers are responsible for restoring vi mocks between tests via
 * `afterEach(() => { ... })`.
 */
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

export async function renderSessionPage(opts: RenderOptions = {}): Promise<RenderHandle> {
  // Reset module-level caches between tests.
  FakeEventSource.reset();
  vi.resetModules();

  // Install fake EventSource on globalThis. Vite/Vitest hoists
  // vi.mock so it must run before importing the page.
  (globalThis as unknown as { EventSource: typeof FakeEventSource }).EventSource = FakeEventSource;

  // jsdom does not implement ResizeObserver — AssistantThread uses
  // one to track the scroll viewport. A minimal no-op stub is enough.
  if (typeof (globalThis as unknown as { ResizeObserver?: unknown }).ResizeObserver === 'undefined') {
    class StubResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
    (globalThis as unknown as { ResizeObserver: typeof StubResizeObserver }).ResizeObserver = StubResizeObserver;
  }

  // Stub the api module — page imports api.* directly for a few calls.
  const apiStub = makeApiStub();
  vi.doMock('../../../lib/api', async () => {
    const real = await vi.importActual<typeof import('../../../lib/api')>(
      '../../../lib/api',
    );
    return { ...real, api: { ...real.api, ...apiStub } };
  });

  // Stub useCapabilities so we don't fire a real /api/capabilities.
  vi.doMock('../../../lib/useCapabilities', () => ({
    useCapabilities: () => ({ platforms: [{ id: 'opencode', displayName: 'OpenCode', available: true, capabilities: opts.caps ?? fullCaps() }] }),
    usePlatformCapabilities: () => opts.caps ?? fullCaps(),
    useMultiPlatform: () => false,
    useWorktreeSessions: () => false,
  }));

  // Stub useTmux: report unavailable so tmux-only branches stay quiet.
  vi.doMock('../../../lib/useTmux', () => ({
    useTmux: () => ({
      available: false,
      isLocal: false,
      sessions: [] as TmuxSession[],
      clients: [],
      switchSession: vi.fn().mockResolvedValue(undefined),
      findSession: () => undefined,
      launchOpencode: vi.fn().mockResolvedValue({ session: '' }),
    }),
  }));

  // Stub useGitInfo: no live git checks. The real hook returns
  // `{ infos: Record<string, GitInfo>, loading, error }` and the page
  // looks up `infos[directory]` per row.
  vi.doMock('../../../lib/useGitInfo', () => ({
    useGitInfo: () => ({ infos: {}, loading: false, error: null }),
  }));

  // Stub the changes / info / diff hooks so RightPanel doesn't error.
  vi.doMock('../../../lib/useSessionChanges', () => ({
    useSessionChanges: () => ({
      data: { sessionId: 'sess_1', supported: false, totalAdditions: 0, totalDeletions: 0, filesChanged: 0, files: [] },
      loading: false,
      error: null,
      refresh: vi.fn(),
    }),
  }));
  vi.doMock('../../../lib/useSessionInfo', () => ({
    useSessionInfo: () => ({
      data: null,
      loading: false,
      error: null,
      refresh: vi.fn(),
    }),
  }));
  vi.doMock('../../../lib/useWorkingTreeDiff', () => ({
    useWorkingTreeDiff: () => ({
      data: { repo: '', branch: '', ahead: 0, behind: 0, files: [], truncated: false } as WorkingTreeDiff,
      loading: false,
      error: null,
      notRepo: false,
      refresh: vi.fn(),
    }),
  }));

  // Stub favicon / toast hooks — they touch the document title.
  vi.doMock('../../../lib/useFaviconNotify', () => ({
    recheckFaviconNotify: vi.fn(),
  }));
  vi.doMock('../../../lib/useToastNotify', () => ({
    notifyPromptDismissed: vi.fn(),
  }));

  // Stub TanStack Query hooks used elsewhere — not needed by the page itself.

  // Now lazily import the page and the apiStore so all vi.doMock
  // registrations apply.
  const { useApiStore } = await import('../../../lib/apiStore');
  const { SessionDetail } = await import('../SessionDetail');

  // Build the store action spies and merge into the real store. Each
  // spy delegates to a fixture by default; tests can override via
  // opts.storeOverrides.
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

  // Apply spies to the real store. Zustand merges the patch onto the
  // existing slice so untouched selectors keep working.
  useApiStore.setState({
    ...storeSpies,
    ...(opts.storeOverrides ?? {}),
  } as unknown as Parameters<typeof useApiStore.setState>[0]);

  const sessionId = opts.sessionId ?? detail.session.id;
  const result = render(
    <MemoryRouter initialEntries={[`/session/${sessionId}`]}>
      <Routes>
        <Route path="/session/:id" element={<SessionDetail />} />
      </Routes>
    </MemoryRouter>,
  );

  return {
    result,
    store: storeSpies,
    api: apiStub,
    sse: () => FakeEventSource.latest(),
  };
}

/** Convenience: yield to the microtask queue so promises settle. */
export async function flushPromises(times = 4) {
  for (let i = 0; i < times; i++) await Promise.resolve();
}
