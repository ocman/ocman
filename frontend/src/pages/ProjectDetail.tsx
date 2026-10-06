import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { usePageTitle } from '../lib/headerContext';
import { SessionTable } from '../components/SessionTable';
import { HeaderPortal } from './session-detail/MobileHeaderControls';
import { TimeRangeControl } from '../components/TimeRangeControl';
import { useTmux } from '../lib/useTmux';
import { useOpencodeLaunch } from '../lib/useCapabilities';
import { useClickOutside } from '../lib/useClickOutside';
import { cleanTitle, fuzzyMatch, shortPath } from '../lib/format';
import { openVSCode } from '../lib/shortcuts';
import { useShortcut } from '../lib/shortcutRegistry';
import { useProjects, useSessions } from '../lib/queries';
import { projectIdentityIndex } from '../lib/projectIdentity';
import { remoteLog } from '../lib/remoteLog';
import type { TmuxClient } from '../lib/api';
import { Button, ButtonGroup, SearchField } from '../components/Control';
import styles from './ProjectDetail.module.css';

const DEFAULT_TIME_RANGE = 168; // 7d

// Popover shown when a non-local tmux has multiple attached clients and
// the user must pick which one to switch. Extracted from ProjectDetail
// to keep that component within the size budget.
function TmuxClientPicker({
  pickerRef,
  pos,
  clients,
  onSelect,
}: {
  pickerRef: React.RefObject<HTMLDivElement | null>;
  pos: { top: number; left: number };
  clients: TmuxClient[];
  onSelect: (tty: string) => void;
}) {
  return (
    <div
      ref={pickerRef}
      className="tmux-client-popover"
      style={{ top: pos.top, left: pos.left }}
    >
      <div className="tmux-client-picker-header">
        <span>Select tmux client</span>
      </div>
      {clients.map((c) => (
        <div
          key={c.tty}
          className="tmux-client-picker-item"
          onClick={() => onSelect(c.tty)}
        >
          <span className="tmux-client-tty">{c.tty}</span>
          <span className="tmux-client-session">{shortPath(c.session)}</span>
          <span className="tmux-client-size">{c.width}&times;{c.height}</span>
        </div>
      ))}
    </div>
  );
}

export function ProjectDetail() {
  const { dir } = useParams();
  const directory = dir ? decodeURIComponent(dir) : undefined;
  const projectName = directory?.split('/').pop() || 'Project';
  usePageTitle(projectName);
  const navigate = useNavigate();
  const tmux = useTmux();
  const matchingTmuxSession = directory ? tmux.findSession(directory) : undefined;
  const [pendingTmuxSession, setPendingTmuxSession] = useState<string | null>(null);
  const [pickerPos, setPickerPos] = useState<{ top: number; left: number } | null>(null);
  const pickerRef = useRef<HTMLDivElement>(null);

  // Filter state (mirrors the dashboard Sessions tab) — persisted in the
  // URL so refresh / back-forward keep the user's view. Default to 7d
  // (`t` absent ⇒ 168) and to "include archived" (`a` absent ⇒ false),
  // since users navigating into a specific project usually want to see
  // everything that's happened there, archived sessions included.
  const [searchParams, setSearchParams] = useSearchParams();
  const timeRange = parseInt(searchParams.get('t') || String(DEFAULT_TIME_RANGE), 10);
  const excludeArchived = searchParams.get('a') === '1';
  // Carry an explicit owner on to the Worktrees view (absent = this machine).
  const ownerId = searchParams.get('remoteId');
  const ownerQuery = ownerId ? `?remoteId=${encodeURIComponent(ownerId)}` : '';
  const launchAllowed = useOpencodeLaunch(ownerId ?? undefined);

  const setTimeRange = useCallback((v: number) => {
    setSearchParams((p) => { p.set('t', String(v)); return p; }, { replace: true });
  }, [setSearchParams]);

  const setExcludeArchived = useCallback((v: boolean) => {
    setSearchParams((p) => {
      if (v) p.set('a', '1');
      else p.delete('a');
      return p;
    }, { replace: true });
  }, [setSearchParams]);

  useClickOutside(pickerRef, !!pendingTmuxSession, () => setPendingTmuxSession(null));

  // TanStack Query handles dedup, cancellation, stale-while-revalidate,
  // and visibility pausing automatically (Wave 3 / P4+P5 fix).
  // sinceHours produces a stable query key; the actual timestamp is
  // computed inside the queryFn at fetch time.
  const sinceHours = timeRange > 0 ? timeRange : undefined;
  const projectsQ = useProjects();
  const identity = useMemo(() => projectIdentityIndex(projectsQ.data ?? []), [projectsQ.data]);
  const sessionsQ = useSessions(
    { sinceHours, limit: 0 },
    { refetchInterval: 5000, enabled: !!directory },
  );
  const projectKey = identity(directory ?? '', ownerId ?? undefined).key;
  const sessions = (sessionsQ.data ?? []).filter(s => identity(s.directory || '', s.remoteId).key === projectKey);
  const sessionsLoaded = !sessionsQ.isLoading && !projectsQ.isLoading;
  const [search, setSearch] = useState('');
  const q = search.trim();
  const filteredSessions = q
    ? sessions.filter((s) => fuzzyMatch(q, `${cleanTitle(s.title)} ${s.directory}`))
    : sessions;

  const handleTmuxSwitch = useCallback((anchor?: HTMLElement | null) => {
    if (!matchingTmuxSession) return;
    if (tmux.isLocal) {
      tmux.switchSession(matchingTmuxSession.name).catch(err => remoteLog.error('tmux switch failed', err));
      return;
    }
    if (tmux.clients.length === 1) {
      tmux.switchSession(matchingTmuxSession.name, tmux.clients[0].tty).catch(err => remoteLog.error('tmux switch failed', err));
      return;
    }

    const rect = anchor?.getBoundingClientRect();
    setPickerPos(rect ? { top: rect.bottom + 4, left: rect.right } : { top: 88, left: Math.min(window.innerWidth - 24, 420) });
    setPendingTmuxSession(matchingTmuxSession.name);
  }, [matchingTmuxSession, tmux]);

  const handleClientSelect = useCallback((clientTTY: string) => {
    if (!pendingTmuxSession) return;
    tmux.switchSession(pendingTmuxSession, clientTTY).catch(err => remoteLog.error('tmux switch failed', err));
    setPendingTmuxSession(null);
  }, [pendingTmuxSession, tmux]);

  const handleOpenVSCode = useCallback(() => {
    if (!directory) return;
    openVSCode(directory);
  }, [directory]);

  const handleOpenVSCodeRef = useRef(handleOpenVSCode);
  useEffect(() => { handleOpenVSCodeRef.current = handleOpenVSCode; }, [handleOpenVSCode]);
  const directoryRef = useRef(directory);
  useEffect(() => { directoryRef.current = directory; }, [directory]);

  const openVscodeShortcut = useMemo(() => ({
    id: 'project.open-vscode',
    scope: 'project' as const,
    keys: { code: 'KeyV', alt: true },
    description: 'Open current project in VS Code',
    enabled: () => !!directoryRef.current,
    handler: () => handleOpenVSCodeRef.current(),
  }), []);

  useShortcut(openVscodeShortcut);

  return (
    <div>
      {pendingTmuxSession && pickerPos && (
        <TmuxClientPicker
          pickerRef={pickerRef}
          pos={pickerPos}
          clients={tmux.clients}
          onSelect={handleClientSelect}
        />
      )}
      <HeaderPortal>
        <ButtonGroup label="Project actions">
          {matchingTmuxSession && (
            <Button
              type="button"
              size="small"
              onClick={(e) => handleTmuxSwitch(e.currentTarget)}
              title={`Switch tmux to ${shortPath(matchingTmuxSession.name)} (T)`}
            >tmux</Button>
          )}
          {directory && (
            <Button type="button" size="small" onClick={handleOpenVSCode} title="Open in VS Code (V)">VS Code</Button>
          )}
          {directory && launchAllowed && (
            <Button
              type="button"
              size="small"
              onClick={() => navigate(`/project/${encodeURIComponent(directory)}/worktrees${ownerQuery}`)}
              title="View project worktrees"
            >
              Worktrees
            </Button>
          )}
          {directory && (
            <Button
              type="button"
              size="small"
              onClick={() => navigate(`/project/${encodeURIComponent(directory)}/settings`)}
              title="Project settings"
            >
              Settings
            </Button>
          )}
        </ButtonGroup>
      </HeaderPortal>
      <div className={styles.searchBar}>
        <SearchField className={styles.search}
          placeholder="Search sessions…"
          aria-label="Search sessions"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>
      <ButtonGroup label="Project session filters" className={styles.range}>
        <TimeRangeControl value={timeRange} onChange={setTimeRange} />
        <Button type="button" size="small" variant={excludeArchived ? 'accent' : 'default'} aria-pressed={excludeArchived}
          onClick={() => setExcludeArchived(!excludeArchived)}
        >Exclude archived</Button>
      </ButtonGroup>
      <SessionTable sessions={filteredSessions} showProject={false} loading={!sessionsLoaded} includeArchived={!excludeArchived} />
    </div>
  );
}
