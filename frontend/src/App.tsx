import { useCallback, useEffect, useMemo } from 'react';
import type { ReactNode } from 'react';
import { BrowserRouter, Routes, Route, Link, Navigate, useLocation, useNavigate } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useHotkeys } from 'react-hotkeys-hook';
import { DashboardLayout, SessionsTab, ProjectsTab, StatsTab, UsageTab, SettingsTab } from './pages/Dashboard';
import { ProjectDetail } from './pages/ProjectDetail';
import { WorktreesView } from './pages/WorktreesView';
import { Loops } from './pages/Loops';
import { SessionDetail } from './pages/session-detail';
import { SharedConversationView } from './pages/SharedConversationView';
import { Login } from './pages/Login';
import { onSessionChanged } from './lib/useGlobalEvents';
import { HeaderProvider } from './lib/HeaderProvider';
import { useHeaderInfo } from './lib/headerContext';
import { CommandPalette } from './components/CommandPalette';
import { WorktreeFormModal } from './components/WorktreeFormModal';
import { MachinePickerModal } from './components/MachinePickerModal';
import { PlatformBadge } from './components/PlatformBadge';
import { HostBadge } from './components/HostBadge';
import { KeyboardShortcutsDialog } from './components/KeyboardShortcutsDialog';
import { ErrorBoundary } from './components/ErrorBoundary';
import { useFaviconNotify } from './lib/useFaviconNotify';
import { useBellNotify } from './lib/useBellNotify';
import { useNotificationNotify } from './lib/useNotificationNotify';
import { PromptToastNotify } from './components/PromptToastNotify';
import { LaunchProgressOverlay } from './components/LaunchProgressOverlay';
import { useAuthStore } from './lib/authStore';
import { useUiStore } from './lib/uiStore';
import { useShortcut, useShortcutDispatcher } from './lib/shortcutRegistry';
import { useApiStore } from './lib/apiStore';
import { useSessions } from './lib/queries';
import { remoteLog } from './lib/remoteLog';
import { usePerformanceCleanup } from './lib/usePerformanceCleanup';
import { useMemoryMonitor } from './lib/useMemoryMonitor';
import { useLongTaskMonitor } from './lib/useLongTaskMonitor';
import { installDevHandle as installPerfDevHandle } from './lib/perfRing';

// Top-level boundary keyed on the current pathname so navigating away from
// a crashed route auto-recovers without forcing the user to reload. Inner
// boundaries (RightPanel panes, AssistantThread, Composer, Dashboard tabs)
// catch crashes more locally so a single broken view doesn't blank the
// whole app.
function RoutesBoundary({ children }: { children: ReactNode }) {
  const location = useLocation();
  return (
    <ErrorBoundary name="app:routes" resetKey={location.pathname}>
      {children}
    </ErrorBoundary>
  );
}

function Header() {
  const location = useLocation();
  const path = location.pathname;
  const { info } = useHeaderInfo();
  const routeSessionId = path.startsWith('/session/')
    ? decodeURIComponent(path.slice('/session/'.length).split('/')[0])
    : undefined;
  const sessionInfo = routeSessionId && info.sessionId === routeSessionId ? info : {};

  let breadcrumb: React.ReactNode = '';
  if (routeSessionId) {
    breadcrumb = (
      <>
        {sessionInfo.sessionTitle && (
          <>
            {sessionInfo.sessionPlatform && (
              <>
                <PlatformBadge platform={sessionInfo.sessionPlatform} />{' '}
              </>
            )}
            {sessionInfo.sessionTitle}
          </>
        )}
      </>
    );
  } else if (path.startsWith('/project/')) {
    const dir = decodeURIComponent(path.slice('/project/'.length).split('/')[0]);
    const name = dir.split('/').pop();
    breadcrumb = <>{name}</>;
  }

  // Right-hand side of the header: the project path for the current
  // session. The richer per-session stats (Duration / Messages /
  // Tokens / Changes / Cost) were moved to the right-panel
  // "Session info" pane (SessionInfoSidebar); only Project stays in
  // the header because it anchors the page at a glance.
  return (
    <header>
      <h1 style={{ display: 'flex', alignItems: 'center', gap: '0.4em' }}>
        <Link
          to="/"
          style={{ color: 'inherit', textDecoration: 'none', display: 'inline-flex', alignItems: 'center' }}
        >
          <img src="/favicon.svg" alt="ocman" width={20} height={20} style={{ display: 'block' }} />
        </Link>
        <span>{breadcrumb}</span>
      </h1>
      <div className="header-right">
        {routeSessionId && sessionInfo.sessionProject && (
          <span
            className="header-project"
            title={sessionInfo.sessionProjectFull || sessionInfo.sessionProject}
          >
            <HostBadge
              remoteName={sessionInfo.sessionRemoteName}
              remoteId={sessionInfo.sessionRemoteId}
              stale={sessionInfo.sessionRemoteStale}
            />
            {sessionInfo.sessionProject}
          </span>
        )}
        {/* Portal target for per-route header action buttons (tmux,
         * launch, VS Code, new session). SessionDetail mounts its
         * action strip here via createPortal so the buttons appear
         * stacked under the project name instead of hovering over
         * the conversation. */}
        <div id="header-actions-slot" className="header-actions" />
      </div>
    </header>
  );
}

function GlobalHotkeys() {
  const {
    shortcutsOpen,
    toggleShortcuts,
    closeShortcuts,
  } = useUiStore();
  const navigate = useNavigate();

  // Single dispatcher for every shortcut registered via useShortcut.
  useShortcutDispatcher();

  // Alt+Shift+N — reopen the most recently closed (archived) session.
  // Pops the closed-session stack, unarchives it on the server, flips the
  // optimistic flag in the recent-sessions list, then navigates to it.
  const reopenClosedSession = useCallback(() => {
    const { popClosedSession, archiveSession, patchRecentSession } = useApiStore.getState();
    const closed = popClosedSession();
    if (!closed) return;
    archiveSession(closed.platform, closed.id, closed.timeUpdated, false).catch((err) => {
      remoteLog.error('Failed to reopen closed session', err);
    });
    patchRecentSession(closed.id, { archived: false });
    navigate(`/session/${closed.id}`);
  }, [navigate]);

  const scrollHalfPage = useCallback((e: KeyboardEvent) => {
    let el: Element | null = document.activeElement;
    let scroller: Element | null = null;
    while (el && el !== document.documentElement) {
      const style = getComputedStyle(el);
      const overflow = style.overflowY;
      if ((overflow === 'auto' || overflow === 'scroll') && el.scrollHeight > el.clientHeight) {
        scroller = el;
        break;
      }
      el = el.parentElement;
    }
    if (!scroller) scroller = document.querySelector('.oc-thread-viewport') ?? document.documentElement;

    const amount = scroller.clientHeight / 2;
    scroller.scrollBy({ top: e.code === 'ArrowDown' ? amount : -amount, behavior: 'smooth' });
  }, []);

  const toggleShortcutsShortcut = useMemo(() => ({
    id: 'site.toggle-shortcuts',
    scope: 'site' as const,
    keys: [
      { code: 'Slash', alt: true, shift: true },
      { code: 'Slash', alt: true },
      { code: 'IntlRo', alt: true, shift: true },
      { code: 'IntlRo', alt: true },
    ],
    description: 'Open keyboard shortcuts',
    handler: toggleShortcuts,
  }), [toggleShortcuts]);

  const scrollDownShortcut = useMemo(() => ({
    id: 'site.scroll-down',
    scope: 'site' as const,
    keys: { code: 'ArrowDown', alt: true },
    description: 'Scroll down half a page',
    handler: scrollHalfPage,
  }), [scrollHalfPage]);

  const scrollUpShortcut = useMemo(() => ({
    id: 'site.scroll-up',
    scope: 'site' as const,
    keys: { code: 'ArrowUp', alt: true },
    description: 'Scroll up half a page',
    handler: scrollHalfPage,
  }), [scrollHalfPage]);

  const commandPaletteShortcut = useMemo(() => ({
    id: 'site.command-palette',
    scope: 'site' as const,
    keys: { code: 'Space', alt: true },
    label: 'Alt+Space',
    description: 'Open command palette',
    handler: () => {
      useUiStore.getState().openPalette('command');
    },
    runInEditable: true,
  }), []);

  const searchPaletteShortcut = useMemo(() => ({
    id: 'site.search-palette',
    scope: 'site' as const,
    keys: { code: 'KeyF', alt: true },
    label: 'Alt+F',
    description: 'Search sessions',
    handler: () => useUiStore.getState().openPalette('search'),
    runInEditable: true,
  }), []);

  const projectPaletteShortcut = useMemo(() => ({
    id: 'site.project-palette',
    scope: 'site' as const,
    keys: { code: 'KeyN', alt: true },
    label: 'Alt+N',
    description: 'Create new session in project',
    handler: () => useUiStore.getState().openPalette('project-session'),
    runInEditable: true,
  }), []);

  const reopenClosedShortcut = useMemo(() => ({
    id: 'site.reopen-closed-session',
    scope: 'site' as const,
    keys: { code: 'KeyN', alt: true, shift: true },
    label: 'Alt+Shift+N',
    description: 'Reopen last closed session',
    enabled: () => useApiStore.getState().closedSessionStack.length > 0,
    handler: reopenClosedSession,
    runInEditable: true,
  }), [reopenClosedSession]);

  useShortcut(toggleShortcutsShortcut);
  useShortcut(scrollDownShortcut);
  useShortcut(scrollUpShortcut);
  useShortcut(commandPaletteShortcut);
  useShortcut(searchPaletteShortcut);
  useShortcut(projectPaletteShortcut);
  useShortcut(reopenClosedShortcut);

  useHotkeys('esc', () => closeShortcuts(), {
    enabled: shortcutsOpen,
    enableOnFormTags: ['INPUT', 'TEXTAREA', 'SELECT'],
    enableOnContentEditable: true,
    preventDefault: true,
  }, [closeShortcuts, shortcutsOpen]);

  return (
    <>
      <CommandPalette />
      <WorktreeFormModal />
      <MachinePickerModal />
      <KeyboardShortcutsDialog open={shortcutsOpen} onClose={closeShortcuts} />
    </>
  );
}

function FaviconNotify() {
  useFaviconNotify();
  return null;
}

function BellNotify() {
  useBellNotify();
  return null;
}

function NotificationNotify() {
  useNotificationNotify();
  return null;
}

// Listens for `ocman:navigate` messages posted by the service worker
// when the user clicks a notification. The SW prefers postMessage over
// a hard navigation so we keep the SPA's client-side routing (and
// don't blow away unsaved Composer drafts on the way to a session).
function ServiceWorkerNavListener() {
  const navigate = useNavigate();
  useEffect(() => {
    if (typeof navigator === 'undefined' || !navigator.serviceWorker) return;
    function onMessage(event: MessageEvent) {
      const data = event.data as { type?: string; url?: string } | null;
      if (!data || data.type !== 'ocman:navigate' || typeof data.url !== 'string') return;
      navigate(data.url);
    }
    navigator.serviceWorker.addEventListener('message', onMessage);
    return () => navigator.serviceWorker.removeEventListener('message', onMessage);
  }, [navigate]);
  return null;
}

function PerformanceCleanup() {
  usePerformanceCleanup();
  return null;
}

function MemoryMonitor() {
  useMemoryMonitor();
  return null;
}

// Mounts the global longtask observer at app boot so we capture
// main-thread stalls regardless of whether BackendStats is rendered.
// The hook is idempotent — BackendStats can also call useLongTaskMonitor
// to read the same shared stats.
function LongTaskMonitor() {
  useLongTaskMonitor();
  return null;
}

// Installs `window.__ocmanPerf` so operators can inspect recent API
// call timings from the browser devtools console:
//
//   __ocmanPerf.summary()        // grouped per-endpoint percentiles
//   console.table(__ocmanPerf.entries())
//   __ocmanPerf.clear()          // start fresh before reproducing a stall
//
// The ring is populated by every fetchJSON / postJSON call (see
// lib/api.ts), so it works as soon as the app has made at least one
// request.
function PerfDevHandle() {
  useEffect(() => {
    installPerfDevHandle();
  }, []);
  return null;
}

/**
 * AuthGate short-circuits the app tree while the initial auth probe
 * is in flight, and again whenever the client is unauthenticated
 * against an auth-required server. The inner app is only rendered
 * once the gate decides it's safe — this is also what prevents every
 * store's initial fetch from firing into a 401 storm on page load.
 */
function AuthGate({ children }: { children: ReactNode }) {
  const checking = useAuthStore((s) => s.checking);
  const authRequired = useAuthStore((s) => s.authRequired);
  const authenticated = useAuthStore((s) => s.authenticated);
  const bootstrap = useAuthStore((s) => s.bootstrap);

  useEffect(() => {
    bootstrap();
  }, [bootstrap]);

  if (checking) {
    return <div className="oc-login-bootstrap">Checking authentication…</div>;
  }
  if (authRequired && !authenticated) {
    return <Login />;
  }
  return <>{children}</>;
}

// Shared QueryClient for TanStack Query. Sensible defaults:
// - staleTime: 10s — data is considered fresh for 10s after fetch,
//   so rapid navigation doesn't re-fetch immediately.
// - No auto-retry on 4xx (client errors are not transient).
// - Retry once on 5xx / network errors.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      retry: (failureCount, error) => {
        // Don't retry client errors (4xx) or aborts.
        if (error instanceof DOMException && error.name === 'AbortError') return false;
        if (error instanceof Error && error.message.match(/^HTTP [45]\d\d/)) {
          const status = parseInt(error.message.slice(5), 10);
          if (status >= 400 && status < 500) return false;
        }
        return failureCount < 1;
      },
    },
  },
});

// Refresh the session list the moment a session is created/changed
// upstream, instead of waiting for the next poll tick. Registered at
// module scope so it's wired once for the app's lifetime; the
// EventSource itself is opened by useGlobalEvents() mounted at the root.
onSessionChanged(() => {
  void queryClient.invalidateQueries({ queryKey: ['sessions'] });
});

export default function App() {
  return (
    <BrowserRouter>
      <QueryClientProvider client={queryClient}>
        <Routes>
          {/* Public, unauthenticated read-only conversation view. */}
          <Route path="/share/:token" element={<SharedConversationView />} />
          {/* Everything else is the authenticated app. */}
          <Route path="*" element={<AuthenticatedApp />} />
        </Routes>
      </QueryClientProvider>
    </BrowserRouter>
  );
}

function AuthenticatedApp() {
  return (
    <AuthGate>
      <HeaderProvider>
        <FaviconNotify />
        <BellNotify />
        <NotificationNotify />
        <PromptToastNotify />
        <LaunchProgressOverlay />
        <ServiceWorkerNavListener />
        <PerformanceCleanup />
        <MemoryMonitor />
        <LongTaskMonitor />
        <PerfDevHandle />
        <GlobalHotkeys />
        <div className="container">
          <Header />
          <div className="content">
            <RoutesBoundary>
              <Routes>
                <Route element={<DashboardLayout />}>
                  <Route path="/" element={<RootRedirect />} />
                  <Route path="/sessions" element={<SessionsTab />} />
                  <Route path="/projects" element={<ProjectsTab />} />
                  <Route path="/stats" element={<StatsTab />} />
                  <Route path="/usage" element={<UsageTab />} />
                  <Route path="/loops" element={<Loops />} />
                  <Route path="/settings" element={<SettingsTab />} />
                </Route>
                <Route path="/project/:dir/worktrees" element={<WorktreesView />} />
                <Route path="/project/:dir/loops" element={<Loops />} />
                <Route path="/project/:dir" element={<ProjectDetail />} />
                <Route path="/session/:id" element={<SessionDetail />} />
              </Routes>
            </RoutesBoundary>
          </div>
        </div>
      </HeaderProvider>
    </AuthGate>
  );
}

export function RootRedirect() {
  const sessionsQ = useSessions({ limit: 1 });
  if (sessionsQ.isLoading) return null;
  const latest = sessionsQ.data?.[0];
  return <Navigate to={latest ? `/session/${latest.id}` : '/session/new'} replace />;
}
