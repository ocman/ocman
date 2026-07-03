import { useState, useEffect, useRef, useMemo, useCallback } from 'react';
import './CommandPalette.css';
import { useNavigate, useLocation } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import Fuse from 'fuse.js';
import { useApiStore } from '../lib/apiStore';
import { useUiStore } from '../lib/uiStore';
import { useWorktreeSessions, useAgentLoops } from '../lib/useCapabilities';
import { cleanTitle, relativeTime, shortPath } from '../lib/format';
import type { Session, Project, DirectoryBrowseEntry, DirectorySearchEntry } from '../lib/api';
import { useTmux } from '../lib/useTmux';
import { createSessionWithLaunch } from '../lib/createSessionWithLaunch';
import { resolveTargetForDir } from '../lib/machinePicker';
import { remoteLog } from '../lib/remoteLog';

type CommandItem = { kind: 'command'; id: string; label: string; description: string };
type ScopedItem = { kind: 'scoped'; id: string; label: string; description: string };
type NavItem = { kind: 'nav'; id: string; label: string; path: string };
type CommandNavItem = CommandItem | ScopedItem | NavItem;

type ResultItem =
  | { kind: 'session'; session: Session }
  | { kind: 'project'; project: Project }
  | { kind: 'browse-parent'; directory: string }
  | { kind: 'browse-directory'; entry: DirectoryBrowseEntry }
  | { kind: 'browse-search-directory'; entry: DirectorySearchEntry }
  | CommandNavItem;

type ProjectBrowserState = {
  open: boolean;
  directory: string;
  parent: string;
  home: string;
  entries: DirectoryBrowseEntry[];
  searchEntries: DirectorySearchEntry[];
  loading: boolean;
  error: string | null;
  searchLoading: boolean;
  searchError: string | null;
};

const CLOSED_PROJECT_BROWSER: ProjectBrowserState = {
  open: false,
  directory: '',
  parent: '',
  home: '',
  entries: [],
  searchEntries: [],
  loading: false,
  error: null,
  searchLoading: false,
  searchError: null,
};

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

// `cmd.loops` is the /loop palette entry. Capability-gated on agentLoops.
const LOOPS_COMMAND: CommandItem = {
  kind: 'command',
  id: 'cmd.loops',
  label: 'loops',
  description: 'View agent loops',
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

function directoryQueryPrefix(directory: string): string {
  if (!directory || directory === '/') return directory;
  return directory.endsWith('/') ? directory : `${directory}/`;
}

function isCurrentDirectoryQuery(query: string, directory: string): boolean {
  const trimmed = query.trim();
  if (!trimmed || !directory) return false;
  return trimmed === directory || trimmed === directoryQueryPrefix(directory);
}

export function CommandPalette() {
  const [query, setQuery] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();
  const location = useLocation();
  const queryClient = useQueryClient();
  const sessions = useApiStore((s) => s.cachedSessions);
  const projects = useApiStore((s) => s.getProjects);
  const browseDirectories = useApiStore((s) => s.browseDirectories);
  const searchDirectories = useApiStore((s) => s.searchDirectories);
  const createSession = useApiStore((s) => s.createSession);
  const launchOpencodeInTmux = useApiStore((s) => s.launchOpencodeInTmux);
  const seedNewSession = useApiStore((s) => s.seedNewSession);
  const refreshCachedSessions = useApiStore((s) => s.refreshCachedSessions);
  const tmux = useTmux();
  const worktreeSessionsAllowed = useWorktreeSessions();
  const agentLoopsAllowed = useAgentLoops();
  const openWorktreeForm = useUiStore((s) => s.openWorktreeForm);
  const {
    paletteOpen,
    paletteMode,
    closePalette: rawClosePalette,
    openProjectSessionPalette,
    openShortcuts,
  } = useUiStore();
  const mode = paletteMode;

  const [projectList, setProjectList] = useState<Project[]>([]);
  const [projectListLoading, setProjectListLoading] = useState(false);
  const [projectListLoaded, setProjectListLoaded] = useState(false);
  const [projectListError, setProjectListError] = useState<string | null>(null);
  const [projectBrowser, setProjectBrowser] = useState<ProjectBrowserState>(CLOSED_PROJECT_BROWSER);
  const projectBrowserAbortRef = useRef<AbortController | null>(null);
  const projectSearchAbortRef = useRef<AbortController | null>(null);

  const resetProjectBrowser = useCallback(() => {
    projectBrowserAbortRef.current?.abort();
    projectSearchAbortRef.current?.abort();
    projectBrowserAbortRef.current = null;
    projectSearchAbortRef.current = null;
    setProjectBrowser(CLOSED_PROJECT_BROWSER);
  }, []);

  const closePalette = useCallback(() => {
    setProjectList([]);
    setProjectListLoading(false);
    setProjectListLoaded(false);
    setProjectListError(null);
    resetProjectBrowser();
    rawClosePalette();
  }, [rawClosePalette, resetProjectBrowser]);

  const openProjectBrowser = useCallback((directory?: string, opts?: { query?: string }) => {
    projectBrowserAbortRef.current?.abort();
    projectSearchAbortRef.current?.abort();
    projectSearchAbortRef.current = null;
    const controller = new AbortController();
    projectBrowserAbortRef.current = controller;
    setQuery(opts?.query ?? '');
    setSelectedIndex(0);
    inputRef.current?.focus();
    setProjectBrowser((prev) => ({
      ...prev,
      open: true,
      loading: true,
      error: null,
      entries: [],
      searchEntries: [],
      searchLoading: false,
      searchError: null,
      directory: directory ?? prev.directory,
    }));

    browseDirectories(directory, controller.signal)
      .then((resp) => {
        if (controller.signal.aborted) return;
        projectBrowserAbortRef.current = null;
        setSelectedIndex(0);
        setProjectBrowser({
          open: true,
          directory: resp.directory,
          parent: resp.parent ?? '',
          home: resp.home ?? '',
          entries: resp.entries,
          searchEntries: [],
          loading: false,
          error: null,
          searchLoading: false,
          searchError: null,
        });
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        projectBrowserAbortRef.current = null;
        const message = err instanceof Error ? err.message : 'Failed to browse directory';
        setProjectBrowser((prev) => ({
          ...prev,
          open: true,
          loading: false,
          error: message,
          searchLoading: false,
        }));
      });
  }, [browseDirectories]);

  // Effective static commands list. WORKTREE_COMMAND only appears when
  // the host supports the /wt feature (capability-gated; AD-7).
  const staticCommands = useMemo(() => {
    const cmds = [...STATIC_COMMANDS];
    if (worktreeSessionsAllowed) cmds.push(WORKTREE_COMMAND);
    if (agentLoopsAllowed) cmds.push(LOOPS_COMMAND);
    return cmds;
  }, [worktreeSessionsAllowed, agentLoopsAllowed]);

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
    if (!paletteOpen || mode !== 'project' || projectBrowser.open || projectBrowser.loading) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect
    openProjectBrowser();
  }, [paletteOpen, mode, projectBrowser.open, projectBrowser.loading, openProjectBrowser]);

  useEffect(() => {
    if (!paletteOpen || mode !== 'project-session') return;
    const controller = new AbortController();
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setProjectListLoading(true);
    setProjectListLoaded(false);
    setProjectListError(null);
    projects(controller.signal)
      .then((list) => {
        if (controller.signal.aborted) return;
        setProjectList(list);
        setProjectListLoading(false);
        setProjectListLoaded(true);
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        const message = err instanceof Error ? err.message : 'Failed to load projects';
        setProjectList([]);
        setProjectListLoading(false);
        setProjectListLoaded(true);
        setProjectListError(message);
      });
    return () => controller.abort();
  }, [paletteOpen, mode, projects]);

  useEffect(() => () => {
    projectBrowserAbortRef.current?.abort();
    projectSearchAbortRef.current?.abort();
  }, []);

  useEffect(() => {
    if (!paletteOpen || mode !== 'project' || !projectBrowser.open) return;
    const searchQuery = query.trim();
    projectSearchAbortRef.current?.abort();

    if (!searchQuery || isCurrentDirectoryQuery(searchQuery, projectBrowser.directory)) {
      return;
    }
    if (!projectBrowser.directory) return;

    const controller = new AbortController();
    projectSearchAbortRef.current = controller;
    const timeout = window.setTimeout(() => {
      setProjectBrowser((prev) => ({
        ...prev,
        searchLoading: true,
        searchError: null,
      }));
      searchDirectories(projectBrowser.directory, searchQuery, 50, controller.signal)
        .then((resp) => {
          if (controller.signal.aborted) return;
          projectSearchAbortRef.current = null;
          setSelectedIndex(0);
          setProjectBrowser((prev) => ({
            ...prev,
            searchEntries: resp.entries,
            searchLoading: false,
            searchError: null,
          }));
        })
        .catch((err) => {
          if (controller.signal.aborted) return;
          projectSearchAbortRef.current = null;
          const message = err instanceof Error ? err.message : 'Failed to search directories';
          setProjectBrowser((prev) => ({
            ...prev,
            searchEntries: [],
            searchLoading: false,
            searchError: message,
          }));
        });
    }, 180);

    return () => {
      window.clearTimeout(timeout);
      controller.abort();
    };
  }, [paletteOpen, mode, projectBrowser.open, projectBrowser.directory, query, searchDirectories]);

  useEffect(() => {
    if (!paletteOpen) return;
    if (mode === 'search') {
      const controller = new AbortController();
      refreshCachedSessions(controller.signal).catch(() => {});
      return () => controller.abort();
    }
  }, [paletteOpen, mode, refreshCachedSessions]);

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
      if (!projectBrowser.open) return [];
      const searching = !!query.trim() && !isCurrentDirectoryQuery(query, projectBrowser.directory);
      if (searching) {
        if (projectBrowser.searchLoading || projectBrowser.searchError) return [];
        return projectBrowser.searchEntries.map((entry) => ({
          kind: 'browse-search-directory' as const,
          entry,
        }));
      }
      if (projectBrowser.loading || projectBrowser.error) return [];
      const browserResults: ResultItem[] = [];
      if (projectBrowser.parent) {
        browserResults.push({ kind: 'browse-parent', directory: projectBrowser.parent });
      }
      for (const entry of projectBrowser.entries) {
        browserResults.push({ kind: 'browse-directory', entry });
      }
      return browserResults;
    }

    if (mode === 'project-session') {
      if (!projectListLoaded || projectListLoading || projectListError) return [];
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
          : `scoped:${item.id}`;
      if (!seen.has(key)) {
        seen.add(key);
        uniqueResults.push(item);
      }
    }

    return uniqueResults;
  }, [mode, query, sessions, sessionFuse, staticCommands, projectBrowser, projectList, projectListLoading, projectListLoaded, projectListError]);

  useEffect(() => {
    if (!listRef.current) return;
    const item = listRef.current.children[selectedIndex] as HTMLElement | undefined;
    item?.scrollIntoView({ block: 'nearest' });
  }, [selectedIndex]);

  function startSessionInDirectory(projectDir: string, opts?: { local?: boolean }) {
    closePalette();
    // Machine-aware create (multi-remote support, AD-15): ask the hub
    // which machine should run this project. Auto-resolves silently on
    // single-host / single-match; prompts when the project lives on
    // several machines or none. Paths picked from the local filesystem
    // browser bypass the resolver because they can only live here.
    const target = opts?.local ? Promise.resolve({ platform: 'opencode', remoteId: 'local' }) : resolveTargetForDir(projectDir);
    void target.then((selectedTarget) => {
      if (selectedTarget === null) return; // operator cancelled the picker
      // Fall back to inferring the platform from an existing session
      // when the resolver returned the empty (local-default) sentinel.
      const chosenPlatform =
        selectedTarget.platform || sessions?.find((s) => s.directory === projectDir)?.platform || '';
      createSessionWithLaunch(
        {
          createSession,
          launchOpencodeInTmux,
          tmuxAvailable: tmux.available,
        },
        { directory: projectDir, platform: chosenPlatform || undefined, remoteId: selectedTarget.remoteId },
      )
        .then((res) => {
          if (res.id) {
            const sessionDir = res.directory ?? projectDir;
            seedNewSession(res.id, sessionDir, chosenPlatform);
            void queryClient.invalidateQueries({ queryKey: ['projects'] });
            void queryClient.invalidateQueries({ queryKey: ['sessions'] });
            navigate(`/session/${res.id}`);
          }
        })
        .catch((err) => remoteLog.error('Failed to create session', err));
    });
  }

  function handleSelect(item: ResultItem) {
    if (item.kind === 'session') {
      closePalette();
      navigate(`/session/${item.session.id}`);
    } else if (item.kind === 'project') {
      startSessionInDirectory(item.project.directory);
    } else if (item.kind === 'browse-parent') {
      openProjectBrowser(item.directory);
    } else if (item.kind === 'browse-directory') {
      openProjectBrowser(item.entry.path);
    } else if (item.kind === 'browse-search-directory') {
      openProjectBrowser(item.entry.path, { query: directoryQueryPrefix(item.entry.path) });
    } else if (item.kind === 'nav') {
      closePalette();
      navigate(item.path);
    } else if (item.kind === 'scoped') {
      if (item.id === 'scoped.new-project') {
        setQuery('');
        setSelectedIndex(0);
        openProjectSessionPalette();
        return;
      }
      closePalette();
      useUiStore.getState().dispatchCommand({ kind: 'scoped', id: item.id, label: item.label, description: item.description });
    } else if (item.kind === 'command') {
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
      } else if (item.id === 'cmd.loops') {
        navigate('/loops');
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

  const projectQueryIsCurrentDirectory =
    mode === 'project' && projectBrowser.open && isCurrentDirectoryQuery(query, projectBrowser.directory);

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
                : mode === 'project'
                ? 'Browse project directories...'
                : 'Select a project to start a session...'
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
        {mode === 'project' && projectBrowser.open && (
          <div className="oc-cmd-browser-bar">
            <button
              type="button"
              className="oc-cmd-browser-icon"
              aria-label="Browse home directory"
              title="Home"
              onClick={() => openProjectBrowser(projectBrowser.home || undefined)}
              disabled={projectBrowser.loading}
            >
              <i className="bi bi-house" aria-hidden="true" />
            </button>
            <span className="oc-cmd-browser-path" title={projectBrowser.directory}>
              {projectBrowser.directory || 'Loading...'}
            </span>
            {projectBrowser.directory && (
              <button
                type="button"
                className="oc-cmd-browser-use"
                onClick={() => startSessionInDirectory(projectBrowser.directory, { local: true })}
              >
                Use this directory
              </button>
            )}
          </div>
        )}
        <div className="oc-cmd-results" ref={listRef}>
          {results.length === 0 && (
            <div className="oc-cmd-empty">
              {mode === 'project' && projectBrowser.open && projectBrowser.loading
                ? 'Loading directories...'
                : mode === 'project' && projectBrowser.open && projectBrowser.error
                ? projectBrowser.error
                : mode === 'project' && projectBrowser.open && query.trim() && !projectQueryIsCurrentDirectory && projectBrowser.searchLoading
                ? 'Searching directories...'
                : mode === 'project' && projectBrowser.open && query.trim() && !projectQueryIsCurrentDirectory && projectBrowser.searchError
                ? projectBrowser.searchError
                : mode === 'project' && projectBrowser.open && query.trim() && !projectQueryIsCurrentDirectory
                ? 'No matching directories'
                : mode === 'project' && projectBrowser.open
                ? 'No child directories'
                : sessions === null && mode === 'search'
                ? 'Loading sessions...'
                : mode === 'project'
                ? 'Loading directories...'
                : mode === 'project-session' && (!projectListLoaded || projectListLoading)
                ? 'Loading projects...'
                : mode === 'project-session' && projectListError
                ? projectListError
                : mode === 'project-session'
                ? 'No projects found'
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
                  key={`project:${proj.directory}`}
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

            if (item.kind === 'browse-parent') {
              return (
                <div
                  key={`browse-parent:${item.directory}`}
                  className={`oc-cmd-item oc-cmd-item--command${i === selectedIndex ? ' oc-cmd-item--selected' : ''}`}
                  onClick={() => handleSelect(item)}
                  onMouseMove={() => setSelectedIndex(i)}
                >
                  <i className="bi bi-arrow-up oc-cmd-item-icon" />
                  <div className="oc-cmd-item-content">
                    <span className="oc-cmd-title">..</span>
                    <span className="oc-cmd-meta">{item.directory}</span>
                  </div>
                </div>
              );
            }

            if (item.kind === 'browse-directory') {
              return (
                <div
                  key={`browse-directory:${item.entry.path}`}
                  className={`oc-cmd-item oc-cmd-item--command${i === selectedIndex ? ' oc-cmd-item--selected' : ''}`}
                  onClick={() => handleSelect(item)}
                  onMouseMove={() => setSelectedIndex(i)}
                >
                  <i className="bi bi-folder oc-cmd-item-icon" />
                  <div className="oc-cmd-item-content">
                    <span className="oc-cmd-title">{item.entry.name}</span>
                    <span className="oc-cmd-meta">{item.entry.path}</span>
                  </div>
                </div>
              );
            }

            if (item.kind === 'browse-search-directory') {
              return (
                <div
                  key={`browse-search-directory:${item.entry.path}`}
                  className={`oc-cmd-item oc-cmd-item--command${i === selectedIndex ? ' oc-cmd-item--selected' : ''}`}
                  onClick={() => handleSelect(item)}
                  onMouseMove={() => setSelectedIndex(i)}
                >
                  <i className={`bi ${item.entry.project ? 'bi-folder-check' : 'bi-folder'} oc-cmd-item-icon`} />
                  <div className="oc-cmd-item-content">
                    <span className="oc-cmd-title">{item.entry.name}</span>
                    <span className="oc-cmd-meta">
                      {item.entry.project ? 'Likely project · ' : ''}{item.entry.path}
                    </span>
                  </div>
                </div>
              );
            }

            if (item.kind === 'command' || item.kind === 'nav' || item.kind === 'scoped') return (
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

            return null;
          })}
        </div>
      </div>
    </div>
  );
}
