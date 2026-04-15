import { Component, useEffect, useState } from 'react';
import type { ReactNode, ErrorInfo } from 'react';
import { BrowserRouter, Routes, Route, Link, useLocation } from 'react-router-dom';
import { useHotkeys } from 'react-hotkeys-hook';
import { Dashboard } from './pages/Dashboard';
import { ProjectDetail } from './pages/ProjectDetail';
import { SessionDetail } from './pages/SessionDetail';
import { HeaderProvider } from './lib/HeaderProvider';
import { useHeaderInfo } from './lib/headerContext';
import { CommandPalette } from './components/CommandPalette';
import { KeyboardShortcutsDialog } from './components/KeyboardShortcutsDialog';
import { isEditableTarget } from './lib/shortcuts';

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

function Header({ onToggleShortcuts }: { onToggleShortcuts: () => void }) {
  const location = useLocation();
  const path = location.pathname;
  const { info } = useHeaderInfo();

  let breadcrumb: React.ReactNode = '';
  if (path.startsWith('/session/')) {
    breadcrumb = (
      <>
        / <Link to="/" style={{ color: 'inherit' }}>Sessions</Link>
        {info.sessionTitle && <> / {info.sessionTitle}</>}
      </>
    );
  } else if (path.startsWith('/project/')) {
    const dir = decodeURIComponent(path.slice('/project/'.length));
    const name = dir.split('/').pop();
    breadcrumb = (
      <>/ <Link to="/" style={{ color: 'inherit' }}>Sessions</Link> / {name}</>
    );
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
      <button
        type="button"
        className="vscode-btn"
        title="Keyboard shortcuts (?)"
        onClick={onToggleShortcuts}
      >?</button>
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
      if (e.defaultPrevented || e.repeat || isEditableTarget(e.target)) return;
      const isPhysicalSlash = e.shiftKey && ((e.code === 'Slash' || e.code === 'IntlRo') || e.keyCode === 191);
      const isQuestionMark = e.key === '?' || (e.shiftKey && e.key === '/') || isPhysicalSlash;
      if (!isQuestionMark || e.metaKey || e.ctrlKey || e.altKey) return;

      e.preventDefault();
      e.stopPropagation();
      onToggleShortcuts();
    };

    document.addEventListener('keydown', onKeyDown, true);

    return () => {
      document.removeEventListener('keydown', onKeyDown, true);
    };
  }, [onToggleShortcuts]);

  useHotkeys('esc', () => onCloseShortcuts(), {
    enabled: shortcutsOpen,
    enableOnFormTags: ['INPUT', 'TEXTAREA', 'SELECT'],
    preventDefault: true,
  }, [onCloseShortcuts, shortcutsOpen]);

  return (
    <>
      <CommandPalette />
      <KeyboardShortcutsDialog open={shortcutsOpen} onClose={onCloseShortcuts} />
    </>
  );
}

export default function App() {
  const [shortcutsOpen, setShortcutsOpen] = useState(false);

  return (
    <BrowserRouter>
      <HeaderProvider>
        <GlobalHotkeys
          shortcutsOpen={shortcutsOpen}
          onToggleShortcuts={() => setShortcutsOpen((open) => !open)}
          onCloseShortcuts={() => setShortcutsOpen(false)}
        />
        <div className="container">
          <Header onToggleShortcuts={() => setShortcutsOpen((open) => !open)} />
          <div className="content">
            <ErrorBoundary>
              <Routes>
                <Route path="/" element={<Dashboard />} />
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
