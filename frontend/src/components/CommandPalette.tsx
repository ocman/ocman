import { useState, useEffect, useId, useRef, useMemo, useCallback } from 'react';
import './CommandPalette.css';
import { useNavigate, useLocation } from 'react-router-dom';
import { useApiStore } from '../lib/apiStore';
import { useUiStore } from '../lib/uiStore';
import { useOpencodeLaunch } from '../lib/useCapabilities';
import { cleanTitle, fuzzyMatch, fuzzyRank, fuzzyScore, relativeTime, shortPath } from '../lib/format';
import { isTerminalStatus } from '../lib/sessionStatus';
import type { Session, Project, DirectoryBrowseEntry, DirectorySearchEntry } from '../lib/api';
import { newSessionPath } from '../lib/newSessionPath';
import { usePluginActions } from '../lib/usePluginActions';
import type { PluginActionRequest } from '../lib/plugins';
import { PluginActionDialog } from './PluginActionDialog';

type CommandItem = { kind: 'command'; id: string; label: string; description: string; run?: () => void };
type ScopedItem = { kind: 'scoped'; id: string; label: string; description: string };
type NavItem = { kind: 'nav'; id: string; label: string; path: string };
type CommandNavItem = CommandItem | ScopedItem | NavItem;

// Title matches outrank directory matches; a query spanning both
// ("ocman fix") still matches via the joined text, ranked lowest.
const sessionScore = (query: string) => (s: Session) => {
  const title = cleanTitle(s.title);
  const best = Math.max(fuzzyScore(query, title), fuzzyScore(query, s.directory) * 0.5);
  return best >= 0 ? best : fuzzyMatch(query, `${title} ${s.directory}`) ? 0 : -1;
};

type ResultItem =
  | { kind: 'session'; session: Session }
  | { kind: 'project'; project: Project }
  | { kind: 'browse-parent'; directory: string }
  | { kind: 'browse-directory'; entry: DirectoryBrowseEntry }
  | { kind: 'browse-search-directory'; entry: DirectorySearchEntry }
  | { kind: 'new-project' }
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
  { kind: 'nav', id: 'nav.analytics', label: 'Analytics', path: '/analytics/overview' },
  { kind: 'nav', id: 'nav.artifacts', label: 'Artifacts', path: '/artifacts' },
];

const STATIC_COMMANDS: CommandItem[] = [
  { kind: 'command', id: 'cmd.sessions', label: 'sessions', description: 'Go to Sessions tab' },
  { kind: 'command', id: 'cmd.projects', label: 'projects', description: 'Go to Projects tab' },
  { kind: 'command', id: 'cmd.analytics', label: 'analytics', description: 'Go to Analytics' },
  { kind: 'command', id: 'cmd.stats', label: 'stats', description: 'Go to Analytics performance' },
  { kind: 'command', id: 'cmd.usage', label: 'usage', description: 'Go to Analytics overview' },
  { kind: 'command', id: 'cmd.shortcuts', label: 'shortcuts', description: 'Open keyboard shortcuts' },
];

// `cmd.worktree` is the /wt palette entry. Listed separately so it can
// be filtered out by useOpencodeLaunch() without mutating
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
  { kind: 'scoped', id: 'scoped.permissions', label: 'permissions', description: 'Change permission mode' },
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
    const key = item.kind === 'command' && item.run ? item.id : item.label.toLowerCase();
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
  // Results are a listbox driven from the input (combobox +
  // aria-activedescendant): focus stays in the search field, which the
  // arrow-key handling already assumes.
  const listId = useId();
  const optionId = (index: number) => `${listId}-option-${index}`;
  const optionProps = (index: number) => ({
    id: optionId(index),
    role: 'option',
    'aria-selected': index === selectedIndex,
  });
  const navigate = useNavigate();
  const location = useLocation();
  const sessions = useApiStore((s) => s.cachedSessions);
  const projects = useApiStore((s) => s.getProjects);
  const browseDirectories = useApiStore((s) => s.browseDirectories);
  const searchDirectories = useApiStore((s) => s.searchDirectories);
  const refreshCachedSessions = useApiStore((s) => s.refreshCachedSessions);
  const launchAllowed = useOpencodeLaunch();
  const openWorktreeForm = useUiStore((s) => s.openWorktreeForm);
  const paletteOpen = useUiStore((s) => s.paletteOpen);
  const paletteMode = useUiStore((s) => s.paletteMode);
  const projectSessionInitialDirectory = useUiStore((s) => s.projectSessionInitialDirectory);
  const rawClosePalette = useUiStore((s) => s.closePalette);
  const openProjectSessionPalette = useUiStore((s) => s.openProjectSessionPalette);
  const openProjectPalette = useUiStore((s) => s.openProjectPalette);
  const openShortcuts = useUiStore((s) => s.openShortcuts);
  const mode = paletteMode;
  const [actionInvocation, setActionInvocation] = useState<{ label: string; request: PluginActionRequest } | null>(null);

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

  const contributions = usePluginActions(inferredProjectDir, paletteOpen && mode === 'command');
  const staticCommands: CommandItem[] = useMemo(() => [
    ...STATIC_COMMANDS,
    ...(launchAllowed ? [WORKTREE_COMMAND] : []),
    ...contributions.actions.map((item): CommandItem => ({
      kind: 'command',
      id: `action:${item.ownerId}:${item.pluginId}:${item.action.id}`,
      label: item.action.label,
      description: `${item.pluginId} · ${item.action.placement}`,
      run: () => setActionInvocation({
        label: item.action.label,
        request: {
          ownerId: item.ownerId,
          pluginId: item.pluginId,
          actionId: item.action.id,
          operationId: crypto.randomUUID(),
          placement: item.action.placement,
          surface: 'command-palette',
          context: contributions.context,
        },
      }),
    })),
  ], [launchAllowed, contributions.actions, contributions.context]);

  useEffect(() => {
    if (!paletteOpen) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setQuery('');
    setSelectedIndex(0);
    // Depend on `mode` too: reopening into a different mode while the palette
    // is already open must clear any leftover query.
  }, [paletteOpen, mode]);

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
        if (projectSessionInitialDirectory) {
          const sorted = [...list].sort((a, b) => b.lastUsed - a.lastUsed);
          const index = sorted.findIndex((project) => project.directory === projectSessionInitialDirectory);
          setSelectedIndex(index < 0 ? 0 : index);
        }
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
  }, [paletteOpen, mode, projectSessionInitialDirectory, projects]);

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
      return fuzzyRank(sessions, sessionScore(query)).slice(0, 20).map((session) => ({
        kind: 'session' as const,
        session,
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
      const q = query.trim().toLowerCase();
      // Recency first so it breaks ties between equally good matches.
      const byRecency = projectList.slice().sort((a, b) => b.lastUsed - a.lastUsed);
      const projects = fuzzyRank(byRecency, (p) => fuzzyScore(q, p.directory))
        .slice(0, 20)
        .map((p) => ({ kind: 'project' as const, project: p }));
      // "Create new project" always stays at the end, unaffected by search.
      return [...projects, { kind: 'new-project' as const }];
    }

    if (!query.trim()) {
      return dedupeCommandNavItems([...SCOPED_COMMANDS, ...staticCommands, ...NAV_ITEMS]);
    }

    if (isCommandQuery(query)) {
      const q = stripCommandPrefix(query).toLowerCase();
      return dedupeCommandNavItems(fuzzyRank([...staticCommands, ...SCOPED_COMMANDS, ...NAV_ITEMS], (item) => fuzzyScore(q, item.label)));
    }

    const q = query.toLowerCase();
    // Commands, scoped commands and nav items rank together; sessions follow.
    const actions = fuzzyRank([...staticCommands, ...SCOPED_COMMANDS, ...NAV_ITEMS], (item) => fuzzyScore(q, item.label));
    const sessionResults = sessions
      ? fuzzyRank(sessions, sessionScore(query)).slice(0, 10).map((session) => ({
          kind: 'session' as const,
          session,
        }))
      : [];

    const uniqueResults: ResultItem[] = [];
    const seen = new Set<string>();
    for (const item of [...actions, ...sessionResults]) {
      const key =
        item.kind === 'session'
          ? `session:${item.session.id}`
          : item.kind === 'command' && item.run
          ? item.id
          : item.kind === 'command' || item.kind === 'nav'
          ? `navcmd:${item.label.toLowerCase()}`
          : `scoped:${item.id}`;
      if (!seen.has(key)) {
        seen.add(key);
        uniqueResults.push(item);
      }
    }

    return uniqueResults;
  }, [mode, query, sessions, staticCommands, projectBrowser, projectList, projectListLoading, projectListLoaded, projectListError]);

  useEffect(() => {
    if (!listRef.current) return;
    const item = listRef.current.children[selectedIndex] as HTMLElement | undefined;
    item?.scrollIntoView({ block: 'nearest' });
  }, [selectedIndex]);

  function startSessionInDirectory(
    projectDir: string,
    opts?: { remoteId?: string; platform?: string },
  ) {
    closePalette();
    // A project row that carries its owning remote targets that machine;
    // everything else starts here. Nothing is created yet: the composer
    // can still move the first prompt to another machine that has the
    // project, or into a fresh worktree.
    navigate(newSessionPath({ directory: projectDir, remoteId: opts?.remoteId || 'local', platform: opts?.platform }));
  }

  function handleSelect(item: ResultItem) {
    if (item.kind === 'session') {
      closePalette();
      navigate(`/session/${item.session.id}`);
    } else if (item.kind === 'project') {
      startSessionInDirectory(item.project.directory, {
        remoteId: item.project.remoteId,
        platform: item.project.platform,
      });
    } else if (item.kind === 'browse-parent') {
      openProjectBrowser(item.directory);
    } else if (item.kind === 'browse-directory') {
      openProjectBrowser(item.entry.path);
    } else if (item.kind === 'browse-search-directory') {
      openProjectBrowser(item.entry.path, { query: directoryQueryPrefix(item.entry.path) });
    } else if (item.kind === 'new-project') {
      openProjectPalette();
      openProjectBrowser();
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
      if (item.run) {
        item.run();
      } else if (item.id === 'cmd.shortcuts') {
        openShortcuts();
      } else if (item.id === 'cmd.worktree') {
        openWorktreeForm({ projectDir: inferredProjectDir });
      } else if (item.id === 'cmd.sessions') {
        navigate('/sessions');
      } else if (item.id === 'cmd.projects') {
        navigate('/projects');
      } else if (item.id === 'cmd.analytics') {
        navigate('/analytics/overview');
      } else if (item.id === 'cmd.stats') {
        navigate('/analytics/performance');
      } else if (item.id === 'cmd.usage') {
        navigate('/analytics/overview');
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

  if (actionInvocation) return <PluginActionDialog key={actionInvocation.request.operationId} {...actionInvocation} onClose={() => setActionInvocation(null)} />;
  if (!paletteOpen) return null;

  const projectQueryIsCurrentDirectory =
    mode === 'project' && projectBrowser.open && isCurrentDirectoryQuery(query, projectBrowser.directory);

  return (
    <div className="oc-cmd-backdrop" onClick={closePalette}>
      <div className="oc-cmd-palette" data-perf="palette" onClick={(e) => e.stopPropagation()}>
        <div className="oc-cmd-input-wrap">
          <i className="bi bi-search oc-cmd-search-icon" />
          <input
            ref={inputRef}
            autoFocus
            className="oc-cmd-input"
            type="text"
            role="combobox"
            aria-label="Search commands and sessions"
            aria-expanded="true"
            aria-autocomplete="list"
            aria-controls={listId}
            aria-activedescendant={results.length > 0 ? optionId(selectedIndex) : undefined}
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
        {contributions.unavailable && <p role="status">Some plugin actions are unavailable.</p>}
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
                onClick={() => startSessionInDirectory(projectBrowser.directory)}
              >
                Use this directory
              </button>
            )}
          </div>
        )}
        <div className="oc-cmd-results" id={listId} role="listbox" aria-label="Results" ref={listRef}>
          {results.length === 0 && (
            <div className="oc-cmd-empty" role="presentation">
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
              const cmdSeen = isTerminalStatus(session.status) && session.seen;
              return (
                <div
                  key={session.id}
                  {...optionProps(i)}
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
                  key={`project:${proj.remoteId ?? 'local'}:${proj.directory}`}
                  {...optionProps(i)}
                  className={`oc-cmd-item oc-cmd-item--command${i === selectedIndex ? ' oc-cmd-item--selected' : ''}`}
                  onClick={() => handleSelect(item)}
                  onMouseMove={() => setSelectedIndex(i)}
                >
                  <i className="bi bi-folder oc-cmd-item-icon" />
                  <div className="oc-cmd-item-content">
                    <span className="oc-cmd-title">
                      {shortPath(proj.directory)}
                      {proj.remoteName ? <span className="oc-cmd-badge">{proj.remoteName}</span> : null}
                    </span>
                    <span className="oc-cmd-meta">
                      {proj.remoteId
                        ? proj.remoteName
                        : `${proj.sessionCount} session${proj.sessionCount !== 1 ? 's' : ''} \u00b7 ${relativeTime(proj.lastUsed)}`}
                    </span>
                  </div>
                </div>
              );
            }

            if (item.kind === 'browse-parent') {
              return (
                <div
                  key={`browse-parent:${item.directory}`}
                  {...optionProps(i)}
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
                  {...optionProps(i)}
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
                  {...optionProps(i)}
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

            if (item.kind === 'new-project') {
              return (
                <div
                  key="new-project"
                  {...optionProps(i)}
                  className={`oc-cmd-item oc-cmd-item--command${i === selectedIndex ? ' oc-cmd-item--selected' : ''}`}
                  onClick={() => handleSelect(item)}
                  onMouseMove={() => setSelectedIndex(i)}
                >
                  <i className="bi bi-plus-circle oc-cmd-item-icon" />
                  <div className="oc-cmd-item-content">
                    <span className="oc-cmd-title">Create new project</span>
                    <span className="oc-cmd-meta">Browse the filesystem to pick a directory</span>
                  </div>
                </div>
              );
            }

            if (item.kind === 'command' || item.kind === 'nav' || item.kind === 'scoped') return (
              <div
                key={item.id}
                {...optionProps(i)}
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
