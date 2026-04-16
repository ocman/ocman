import { Component, useEffect, useState } from 'react';
import type { ReactNode, ErrorInfo } from 'react';
import { BrowserRouter, Routes, Route, Link, useLocation } from 'react-router-dom';
import { useHotkeys } from 'react-hotkeys-hook';
import { DashboardLayout, SessionsTab, ProjectsTab, StatsTab, UsageTab } from './pages/Dashboard';
import { ProjectDetail } from './pages/ProjectDetail';
import { SessionDetail } from './pages/SessionDetail';
import { HeaderProvider } from './lib/HeaderProvider';
import { useHeaderInfo } from './lib/headerContext';
import { CommandPalette } from './components/CommandPalette';
import { KeyboardShortcutsDialog } from './components/KeyboardShortcutsDialog';
import { useFaviconNotify } from './lib/useFaviconNotify';

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
        {info.sessionTitle && <> / {info.sessionTitle}</>}
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

function GlobalHotkeys({ shortcutsOpen, onToggleShortcuts, onCloseShortcuts }: {
  shortcutsOpen: boolean;
  onToggleShortcuts: () => void;
  onCloseShortcuts: () => void;
}) {
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.repeat) return;
      // Alt+? (Alt+Shift+/ on most layouts) — use e.code to handle macOS Option key producing special chars
      const isAltQuestion = e.altKey && e.shiftKey && (e.code === 'Slash' || e.code === 'IntlRo');
      if (!isAltQuestion || e.metaKey || e.ctrlKey) return;

      e.preventDefault();
      e.stopPropagation();
      onToggleShortcuts();
    };

    window.addEventListener('keydown', onKeyDown, true);

    return () => {
      window.removeEventListener('keydown', onKeyDown, true);
    };
  }, [onToggleShortcuts]);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.repeat || !e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;
      if (e.code !== 'ArrowUp' && e.code !== 'ArrowDown') return;
      e.preventDefault();

      // Find the nearest scrollable ancestor of the active/focused element
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
      // Fall back to the thread viewport or document
      if (!scroller) scroller = document.querySelector('.oc-thread-viewport') ?? document.documentElement;

      const amount = scroller.clientHeight / 2;
      scroller.scrollBy({ top: e.code === 'ArrowDown' ? amount : -amount, behavior: 'smooth' });
    };

    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, []);

  useHotkeys('esc', () => onCloseShortcuts(), {
    enabled: shortcutsOpen,
    enableOnFormTags: ['INPUT', 'TEXTAREA', 'SELECT'],
    enableOnContentEditable: true,
    preventDefault: true,
  }, [onCloseShortcuts, shortcutsOpen]);

  return (
    <>
      <CommandPalette />
      <KeyboardShortcutsDialog open={shortcutsOpen} onClose={onCloseShortcuts} />
    </>
  );
}

function FaviconNotify() {
  useFaviconNotify();
  return null;
}

export default function App() {
  const [shortcutsOpen, setShortcutsOpen] = useState(false);

  return (
    <BrowserRouter>
      <HeaderProvider>
        <FaviconNotify />
        <GlobalHotkeys
          shortcutsOpen={shortcutsOpen}
          onToggleShortcuts={() => setShortcutsOpen((open) => !open)}
          onCloseShortcuts={() => setShortcutsOpen(false)}
        />
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
