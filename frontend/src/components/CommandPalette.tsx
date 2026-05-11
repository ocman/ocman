import { useState, useEffect, useRef, useMemo, useCallback } from 'react';
import './CommandPalette.css';
import { useNavigate, useLocation } from 'react-router-dom';
import Fuse from 'fuse.js';
import { useApiStore } from '../lib/apiStore';
import { useUiStore } from '../lib/uiStore';
import { useWorktreeSessions } from '../lib/useCapabilities';
import { cleanTitle, relativeTime, shortPath } from '../lib/format';
import type { Session, Project } from '../lib/api';
import { useTmux } from '../lib/useTmux';
import { createSessionWithLaunch } from '../lib/createSessionWithLaunch';

type CommandItem = { kind: 'command'; id: string; label: string; description: string };
type ScopedItem = { kind: 'scoped'; id: string; label: string; description: string };
type NavItem = { kind: 'nav'; id: string; label: string; path: string };
type CommandNavItem = CommandItem | ScopedItem | NavItem;

type ResultItem =
  | { kind: 'session'; session: Session }
  | { kind: 'project'; project: Project }
  | CommandNavItem;

const NAV_ITEMS: NavItem[] = [
  { kind: 'nav', id: 'nav.sessions', label: 'Sessions', path: '/' },
  { kind: 'nav', id: 'nav.projects', label: 'Projects', path: '/projects' },
  { kind: 'nav', id: 'nav.stats', label: 'Stats', path: '/stats' },
  { kind: 'nav', id: 'nav.usage', label: 'Usage', path: '/usage' },
];

const STATIC_COMMANDS: CommandItem[] = [
  { kind: 'command', id: 'cmd.sessions', label: 'sessions', description: 'Go to Sessions tab' },
  { kind: 'command', id: 'cmd.projects', label: 'projects', description: 'Go to Projects tab' },
  { kind: 'command', id: 'cmd.stats', label: 'stats', description: 'Go to Stats tab' },
  { kind: 'command', id: 'cmd.usage', label: 'usage', description: 'Go to Usage tab' },
  { kind: 'command', id: 'cmd.shortcuts', label: 'shortcuts', description: 'Open keyboard shortcuts' },
];

// `cmd.worktree` is the /wt palette entry. Listed separately so it can
// be filtered out by useWorktreeSessions() without mutating
// STATIC_COMMANDS in place.
const WORKTREE_COMMAND: CommandItem = {
  kind: 'command',
  id: 'cmd.worktree',
  label: 'wt',
  description: 'New worktree session',
};

const SCOPED_COMMANDS: ScopedItem[] = [
  { kind: 'scoped', id: 'scoped.model', label: 'model', description: 'Change model (session-scoped)' },
  { kind: 'scoped', id: 'scoped.agent', label: 'agent', description: 'Switch agent (session-scoped)' },
  { kind: 'scoped', id: 'scoped.variant', label: 'variant', description: 'Change reasoning effort' },
  { kind: 'scoped', id: 'scoped.tmux', label: 'tmux', description: 'Switch tmux session' },
  { kind: 'scoped', id: 'scoped.vscode', label: 'vscode', description: 'Open in VS Code' },
  { kind: 'scoped', id: 'scoped.archive', label: 'archive', description: 'Archive current session' },
  { kind: 'scoped', id: 'scoped.rename', label: 'rename', description: 'Rename session' },
  { kind: 'scoped', id: 'scoped.new-project', label: 'New session in project', description: 'Create new session in project' },
  { kind: 'scoped', id: 'scoped.compact', label: 'compact', description: 'Compact view' },
];

function isCommandQuery(q: string): boolean {
  return q.startsWith('>') || q.startsWith(':');
}

function stripCommandPrefix(q: string): string {
  if (q.startsWith('>') || q.startsWith(':')) {
    return q.slice(1).trimStart();
  }
  return q;
}

function dedupeCommandNavItems(items: CommandNavItem[]): CommandNavItem[] {
  const seen = new Set<string>();
  const out: CommandNavItem[] = [];

  for (const item of items) {
    const key = item.label.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(item);
  }

  return out;
}

export function CommandPalette() {
  const [query, setQuery] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();
  const location = useLocation();
  const sessions = useApiStore((s) => s.cachedSessions);
  const projects = useApiStore((s) => s.getProjects);
  const createSession = useApiStore((s) => s.createSession);
  const launchOpencodeInTmux = useApiStore((s) => s.launchOpencodeInTmux);
  const seedNewSession = useApiStore((s) => s.seedNewSession);
  const refreshCachedSessions = useApiStore((s) => s.refreshCachedSessions);
  const tmux = useTmux();
  const worktreeSessionsAllowed = useWorktreeSessions();
  const openWorktreeForm = useUiStore((s) => s.openWorktreeForm);
  const {
    paletteOpen,
    paletteMode,
    closePalette: rawClosePalette,
    openProjectPalette,
    openShortcuts,
  } = useUiStore();
  const mode = paletteMode;

  // Load projects when opening project palette
  const [projectList, setProjectList] = useState<Project[]>([]);

  // Wrap closePalette to also clear the project list
  const closePalette = useCallback(() => {
    setProjectList([]);
    rawClosePalette();
  }, [rawClosePalette]);

  // Effective static commands list. WORKTREE_COMMAND only appears when
  // the host supports the /wt feature (capability-gated; AD-7).
  const staticCommands = useMemo(
    () => (worktreeSessionsAllowed ? [...STATIC_COMMANDS, WORKTREE_COMMAND] : STATIC_COMMANDS),
    [worktreeSessionsAllowed],
  );

  // Best-effort project inference for `cmd.worktree` so invoking /wt
  // from a project page or session page pre-fills the project field.
  // Falls back to undefined on global pages.
  const inferredProjectDir = useMemo(() => {
    const path = location.pathname;

    // Project detail routes are mounted under /project/<encoded-dir>
    // with optional child paths like /worktrees.
    if (path.startsWith('/project/')) {
      const rest = path.slice('/project/'.length);
      const encodedDir = rest.split('/')[0];
      if (encodedDir) {
        try {
          return decodeURIComponent(encodedDir);
        } catch {
          return undefined;
        }
      }
      return undefined;
    }

    // Session detail route: look up the session in cachedSessions and
    // use its working directory as the inferred project.
    if (path.startsWith('/session/')) {
      const sessionID = path.slice('/session/'.length).split('/')[0];
      const session = sessions?.find((s) => s.id === sessionID);
      return session?.directory;
    }

    return undefined;
  }, [location.pathname, sessions]);

  useEffect(() => {
    if (paletteOpen) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setQuery('');
      setSelectedIndex(0);
      inputRef.current?.focus();
    }
  }, [paletteOpen]);

  useEffect(() => {
    if (!paletteOpen) return;
    if (mode === 'search') {
      const controller = new AbortController();
      refreshCachedSessions(controller.signal).catch(() => {});
      return () => controller.abort();
    }
    if (mode === 'project' && projectList.length === 0) {
      projects().then((list) => {
        setProjectList(list);
      }).catch(() => {});
    }
  }, [paletteOpen, mode, refreshCachedSessions, projects, projectList.length]);

  const sessionFuse = useMemo(
    () =>
      new Fuse(sessions ?? [], {
        keys: [
          { name: 'title', getFn: (s) => cleanTitle(s.title) },
          'directory',
        ],
        threshold: 0.4,
        includeScore: true,
      }),
    [sessions],
  );

  const results: ResultItem[] = useMemo(() => {
    if (mode === 'search') {
      if (!sessions) return [];
      if (!query.trim()) {
        return sessions
          .slice()
          .sort((a, b) => {
              const bucket = 5 * 60 * 1000;
              const ba = Math.floor(a.timeUpdated / bucket);
              const bb = Math.floor(b.timeUpdated / bucket);
              if (bb !== ba) return bb - ba;
              if (a.projectId !== b.projectId) return a.projectId < b.projectId ? -1 : 1;
              return a.title < b.title ? -1 : a.title > b.title ? 1 : 0;
            })
          .slice(0, 20)
          .map((s) => ({ kind: 'session' as const, session: s }));
      }
      return sessionFuse.search(query, { limit: 20 }).map((r) => ({
        kind: 'session' as const,
        session: r.item,
      }));
    }

    if (mode === 'project') {
      if (projectList.length === 0) return [];
      if (!query.trim()) {
        return projectList
          .slice()
          .sort((a, b) => b.lastUsed - a.lastUsed)
          .slice(0, 20)
          .map((p) => ({ kind: 'project' as const, project: p }));
      }
      const q = query.toLowerCase();
      return projectList
        .filter((p) => p.directory.toLowerCase().includes(q))
        .sort((a, b) => b.lastUsed - a.lastUsed)
        .slice(0, 20)
        .map((p) => ({ kind: 'project' as const, project: p }));
    }

    if (!query.trim()) {
      return dedupeCommandNavItems([...SCOPED_COMMANDS, ...staticCommands, ...NAV_ITEMS]);
    }

    if (isCommandQuery(query)) {
      const q = stripCommandPrefix(query).toLowerCase();
      const commands = staticCommands.filter((item) => item.label.toLowerCase().includes(q));
      const scoped = SCOPED_COMMANDS.filter((item) => item.label.toLowerCase().includes(q));
      const navs = NAV_ITEMS.filter((item) => item.label.toLowerCase().includes(q));
      return dedupeCommandNavItems([...commands, ...scoped, ...navs]);
    }

    const q = query.toLowerCase();
    const commands = staticCommands.filter((item) => item.label.toLowerCase().includes(q));
    const navs = NAV_ITEMS.filter((item) => item.label.toLowerCase().includes(q));
    const scoped = SCOPED_COMMANDS.filter((item) => item.label.toLowerCase().includes(q));
    const sessionResults = sessions
      ? sessionFuse.search(query, { limit: 10 }).map((r) => ({
          kind: 'session' as const,
          session: r.item,
        }))
      : [];

    const uniqueResults: ResultItem[] = [];
    const seen = new Set<string>();
    for (const item of [...commands, ...scoped, ...navs, ...sessionResults]) {
      const key =
        item.kind === 'session'
          ? `session:${item.session.id}`
          : item.kind === 'command' || item.kind === 'nav'
          ? `navcmd:${item.label.toLowerCase()}`
          : `${item.kind}:${item.id}`;
      if (!seen.has(key)) {
        seen.add(key);
        uniqueResults.push(item);
      }
    }

    return uniqueResults;
  }, [mode, query, sessions, sessionFuse, projectList, staticCommands]);

  useEffect(() => {
    if (!listRef.current) return;
    const item = listRef.current.children[selectedIndex] as HTMLElement | undefined;
    item?.scrollIntoView({ block: 'nearest' });
  }, [selectedIndex]);

  function handleSelect(item: ResultItem) {
    if (item.kind === 'session') {
      closePalette();
      navigate(`/session/${item.session.id}`);
    } else if (item.kind === 'project') {
      closePalette();
      const projectDir = item.project.directory;
      // Infer the platform from any existing session for this directory.
      const inferredPlatform = sessions?.find((s) => s.directory === projectDir)?.platform ?? '';
      createSessionWithLaunch(
        {
          createSession,
          launchOpencodeInTmux,
          tmuxAvailable: tmux.available,
        },
        { directory: projectDir },
      )
        .then((res) => {
          if (res.id) {
            seedNewSession(res.id, projectDir, inferredPlatform);
            navigate(`/session/${res.id}`);
          }
        })
        .catch(console.error);
    } else if (item.kind === 'nav') {
      closePalette();
      navigate(item.path);
    } else if (item.kind === 'scoped') {
      if (item.id === 'scoped.new-project') {
        setQuery('');
        setSelectedIndex(0);
        setProjectList([]);
        openProjectPalette();
        return;
      }
      closePalette();
      useUiStore.getState().dispatchCommand({ kind: 'scoped', id: item.id, label: item.label, description: item.description });
    } else {
      closePalette();
      if (item.id === 'cmd.shortcuts') {
        openShortcuts();
      } else if (item.id === 'cmd.worktree') {
        openWorktreeForm({ projectDir: inferredProjectDir });
      } else if (item.id === 'cmd.sessions') {
        navigate('/');
      } else if (item.id === 'cmd.projects') {
        navigate('/projects');
      } else if (item.id === 'cmd.stats') {
        navigate('/stats');
      } else if (item.id === 'cmd.usage') {
        navigate('/usage');
      }
    }
  }

  function onInputKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault();
      closePalette();
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      setSelectedIndex((i) => Math.min(i + 1, results.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setSelectedIndex((i) => Math.max(i - 1, 0));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      if (results[selectedIndex]) {
        handleSelect(results[selectedIndex]);
      }
    }
  }

  if (!paletteOpen) return null;

  return (
    <div className="oc-cmd-backdrop" onClick={closePalette}>
      <div className="oc-cmd-palette" onClick={(e) => e.stopPropagation()}>
        <div className="oc-cmd-input-wrap">
          <i className="bi bi-search oc-cmd-search-icon" />
          <input
            ref={inputRef}
            className="oc-cmd-input"
            type="text"
            placeholder={
              mode === 'command'
                ? '> commands, :stats, sessions...'
                : mode === 'search'
                ? 'Search sessions...'
                : 'Select a project to create session in...'
            }
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setSelectedIndex(0);
            }}
            onKeyDown={onInputKeyDown}
          />
          <kbd className="oc-cmd-kbd">ESC</kbd>
        </div>
        <div className="oc-cmd-results" ref={listRef}>
          {results.length === 0 && (
            <div className="oc-cmd-empty">
              {sessions === null && mode === 'search'
                ? 'Loading sessions...'
                : projectList.length === 0 && mode === 'project'
                ? 'Loading projects...'
                : 'No results'}
            </div>
          )}
          {results.map((item, i) => {
            if (item.kind === 'session') {
              const session = item.session;
              const pending = session.pendingPermission || session.pendingQuestion;
              const cmdSeen = (session.status === 'waiting' || session.status === 'error' || session.status === 'done') && session.seen;
              return (
                <div
                  key={session.id}
                  className={`oc-cmd-item${i === selectedIndex ? ' oc-cmd-item--selected' : ''}`}
                  onClick={() => handleSelect(item)}
                  onMouseMove={() => setSelectedIndex(i)}
                >
                  <span
                    className="oc-cmd-status"
                    data-status={pending ? 'pending' : session.status}
                    data-seen={cmdSeen ? 'true' : undefined}
                    title={pending ? 'Waiting for your response' : undefined}
                  />
                  <div className="oc-cmd-item-content">
                    <span className="oc-cmd-title">
                      {cleanTitle(session.title) || 'Untitled'}
                    </span>
                    <span className="oc-cmd-meta">
                      {shortPath(session.directory)} &middot;{' '}
                      {relativeTime(session.timeUpdated)}
                    </span>
                  </div>
                </div>
              );
            }

            if (item.kind === 'project') {
              const proj = item.project;
              return (
                <div
                  key={proj.directory}
                  className={`oc-cmd-item oc-cmd-item--command${i === selectedIndex ? ' oc-cmd-item--selected' : ''}`}
                  onClick={() => handleSelect(item)}
                  onMouseMove={() => setSelectedIndex(i)}
                >
                  <i className="bi bi-folder oc-cmd-item-icon" />
                  <div className="oc-cmd-item-content">
                    <span className="oc-cmd-title">{shortPath(proj.directory)}</span>
                    <span className="oc-cmd-meta">
                      {proj.sessionCount} session{proj.sessionCount !== 1 ? 's' : ''} &middot;{' '}
                      {relativeTime(proj.lastUsed)}
                    </span>
                  </div>
                </div>
              );
            }

            return (
              <div
                key={item.id}
                className={`oc-cmd-item oc-cmd-item--command${i === selectedIndex ? ' oc-cmd-item--selected' : ''}`}
                onClick={() => handleSelect(item)}
                onMouseMove={() => setSelectedIndex(i)}
              >
                <i
                  className={`bi ${item.kind === 'nav' ? 'bi-arrow-right' : item.kind === 'scoped' ? 'bi-gear' : 'bi-terminal'} oc-cmd-item-icon`}
                />
                <div className="oc-cmd-item-content">
                  <span className="oc-cmd-title">{item.label}</span>
                  {(item.kind === 'command' || item.kind === 'scoped') && (
                    <span className="oc-cmd-meta">{item.description}</span>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
