import { Component, useCallback, useEffect, useMemo } from 'react';
import type { ReactNode, ErrorInfo } from 'react';
import { BrowserRouter, Routes, Route, Link, useLocation } from 'react-router-dom';
import { useHotkeys } from 'react-hotkeys-hook';
import { DashboardLayout, SessionsTab, ProjectsTab, StatsTab, UsageTab, SettingsTab } from './pages/Dashboard';
import { ProjectDetail } from './pages/ProjectDetail';
import { SessionDetail } from './pages/SessionDetail';
import { Login } from './pages/Login';
import { HeaderProvider } from './lib/HeaderProvider';
import { useHeaderInfo } from './lib/headerContext';
import { CommandPalette } from './components/CommandPalette';
import { PlatformBadge } from './components/PlatformBadge';
import { KeyboardShortcutsDialog } from './components/KeyboardShortcutsDialog';
import { useFaviconNotify } from './lib/useFaviconNotify';
import { useBellNotify } from './lib/useBellNotify';
import { useAuthStore } from './lib/authStore';
import { useUiStore } from './lib/uiStore';
import { useShortcut, useShortcutDispatcher } from './lib/shortcutRegistry';
import { usePerformanceCleanup } from './lib/usePerformanceCleanup';
import { useMemoryMonitor } from './lib/useMemoryMonitor';

class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };
  static getDerivedStateFromError(error: Error) { return { error }; }
  componentDidCatch(error: Error, info: ErrorInfo) { console.error('Uncaught error:', error, info); }
  render() {
    if (this.state.error) {
      return (
        <div className="oc-error-boundary">
          <h2>Something went wrong</h2>
          <p>{this.state.error.message}</p>
          <button onClick={() => { this.setState({ error: null }); window.location.reload(); }}>
            Reload
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}

function Header() {
  const location = useLocation();
  const path = location.pathname;
  const { info } = useHeaderInfo();

  let breadcrumb: React.ReactNode = '';
  if (path.startsWith('/session/')) {
    breadcrumb = (
      <>
        {info.sessionTitle && (
          <>
            {' / '}
            {info.sessionPlatform && (
              <>
                <PlatformBadge platform={info.sessionPlatform} />{' '}
              </>
            )}
            {info.sessionTitle}
          </>
        )}
      </>
    );
  } else if (path.startsWith('/project/')) {
    const dir = decodeURIComponent(path.slice('/project/'.length));
    const name = dir.split('/').pop();
    breadcrumb = <>/ {name}</>;
  }

  return (
    <header>
      <h1>
        <Link to="/" style={{ color: 'inherit', textDecoration: 'none' }}>ocman</Link>{' '}
        <span>{breadcrumb}</span>
      </h1>
      {path.startsWith('/session/') && info.stats && info.stats.length > 0 && (
        <div className="header-stats">
          {info.stats.map((s, i) => (
            <span key={i} className="header-stat">
              <span className="header-stat-label">{s.label}</span>
              <span className="header-stat-value">{s.value}</span>
            </span>
          ))}
        </div>
      )}
    </header>
  );
}

function GlobalHotkeys() {
  const {
    shortcutsOpen,
    toggleShortcuts,
    closeShortcuts,
  } = useUiStore();

  // Single dispatcher for every shortcut registered via useShortcut.
  useShortcutDispatcher();

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
      // eslint-disable-next-line no-console
      console.log('[ocman] Alt+Space → command palette');
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
    handler: () => useUiStore.getState().openPalette('project'),
    runInEditable: true,
  }), []);

  useShortcut(toggleShortcutsShortcut);
  useShortcut(scrollDownShortcut);
  useShortcut(scrollUpShortcut);
  useShortcut(commandPaletteShortcut);
  useShortcut(searchPaletteShortcut);
  useShortcut(projectPaletteShortcut);

  useHotkeys('esc', () => closeShortcuts(), {
    enabled: shortcutsOpen,
    enableOnFormTags: ['INPUT', 'TEXTAREA', 'SELECT'],
    enableOnContentEditable: true,
    preventDefault: true,
  }, [closeShortcuts, shortcutsOpen]);

  return (
    <>
      <CommandPalette />
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

function PerformanceCleanup() {
  usePerformanceCleanup();
  return null;
}

function MemoryMonitor() {
  useMemoryMonitor();
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

export default function App() {
  return (
    <BrowserRouter>
      <AuthGate>
        <HeaderProvider>
          <FaviconNotify />
          <BellNotify />
          <PerformanceCleanup />
          <MemoryMonitor />
          <GlobalHotkeys />
          <div className="container">
            <Header />
            <div className="content">
              <ErrorBoundary>
                <Routes>
                  <Route element={<DashboardLayout />}>
                    <Route path="/" element={<SessionsTab />} />
                    <Route path="/projects" element={<ProjectsTab />} />
                    <Route path="/stats" element={<StatsTab />} />
                    <Route path="/usage" element={<UsageTab />} />
                    <Route path="/settings" element={<SettingsTab />} />
                  </Route>
                  <Route path="/project/*" element={<ProjectDetail />} />
                  <Route path="/session/:id" element={<SessionDetail />} />
                </Routes>
              </ErrorBoundary>
            </div>
          </div>
        </HeaderProvider>
      </AuthGate>
    </BrowserRouter>
  );
}
