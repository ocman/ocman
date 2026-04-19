import { Component, useCallback } from 'react';
import type { ReactNode, ErrorInfo } from 'react';
import { BrowserRouter, Routes, Route, Link, useLocation } from 'react-router-dom';
import { useHotkeys } from 'react-hotkeys-hook';
import { DashboardLayout, SessionsTab, ProjectsTab, StatsTab, UsageTab } from './pages/Dashboard';
import { ProjectDetail } from './pages/ProjectDetail';
import { SessionDetail } from './pages/SessionDetail';
import { HeaderProvider } from './lib/HeaderProvider';
import { useHeaderInfo } from './lib/headerContext';
import { CommandPalette } from './components/CommandPalette';
import { PlatformBadge } from './components/PlatformBadge';
import { KeyboardShortcutsDialog } from './components/KeyboardShortcutsDialog';
import { useFaviconNotify } from './lib/useFaviconNotify';
import { useUiStore } from './lib/uiStore';
import { useShortcut, useShortcutDispatcher } from './lib/shortcutRegistry';

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
  const { shortcutsOpen, toggleShortcuts, closeShortcuts } = useUiStore();

  // Single dispatcher for every shortcut registered via useShortcut.
  useShortcutDispatcher();

  useShortcut({
    id: 'site.toggle-shortcuts',
    scope: 'site',
    // Accept Alt+? (Shift+/) and Alt+/ on both US (Slash) and JIS (IntlRo)
    // layouts so the help dialog is reachable regardless of keyboard.
    keys: [
      { code: 'Slash', alt: true, shift: true },
      { code: 'Slash', alt: true },
      { code: 'IntlRo', alt: true, shift: true },
      { code: 'IntlRo', alt: true },
    ],
    description: 'Open keyboard shortcuts',
    handler: toggleShortcuts,
  });

  const scrollHalfPage = useCallback((e: KeyboardEvent) => {
    // Find the nearest scrollable ancestor of the active/focused element, or
    // fall back to the thread viewport or document.
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

  useShortcut({
    id: 'site.scroll-down',
    scope: 'site',
    keys: { code: 'ArrowDown', alt: true },
    description: 'Scroll down half a page',
    handler: scrollHalfPage,
  });
  useShortcut({
    id: 'site.scroll-up',
    scope: 'site',
    keys: { code: 'ArrowUp', alt: true },
    description: 'Scroll up half a page',
    handler: scrollHalfPage,
  });

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

export default function App() {
  return (
    <BrowserRouter>
      <HeaderProvider>
        <FaviconNotify />
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
                </Route>
                <Route path="/project/*" element={<ProjectDetail />} />
                <Route path="/session/:id" element={<SessionDetail />} />
              </Routes>
            </ErrorBoundary>
          </div>
        </div>
      </HeaderProvider>
    </BrowserRouter>
  );
}
