import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { usePageTitle } from '../lib/headerContext';
import { SessionTable } from '../components/SessionTable';
import { useTmux } from '../lib/useTmux';
import { useWorktreeSessions } from '../lib/useCapabilities';
import { shortPath } from '../lib/format';
import { openVSCode } from '../lib/shortcuts';
import { useShortcut } from '../lib/shortcutRegistry';
import { useSessions } from '../lib/queries';
import { remoteLog } from '../lib/remoteLog';
import type { TmuxClient } from '../lib/api';
// ProjectDetail is mounted outside DashboardLayout, so we need to pull in
// Dashboard.css explicitly to get the .oc-time-range / .oc-time-range-btn
// styles used by the filter bar below.
import './Dashboard.css';

const TIME_RANGE_OPTIONS = [
  { label: '12h', value: 12 },
  { label: '24h', value: 24 },
  { label: '7d', value: 168 },
  { label: '30d', value: 720 },
  { label: 'All', value: 0 },
];

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
  const worktreeSessionsAllowed = useWorktreeSessions();
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

  useEffect(() => {
    if (!pendingTmuxSession) return;
    const handle = (e: MouseEvent) => {
      if (pickerRef.current && !pickerRef.current.contains(e.target as Node)) {
        setPendingTmuxSession(null);
      }
    };
    document.addEventListener('mousedown', handle);
    return () => document.removeEventListener('mousedown', handle);
  }, [pendingTmuxSession]);

  // TanStack Query handles dedup, cancellation, stale-while-revalidate,
  // and visibility pausing automatically (Wave 3 / P4+P5 fix).
  // sinceHours produces a stable query key; the actual timestamp is
  // computed inside the queryFn at fetch time.
  const sinceHours = timeRange > 0 ? timeRange : undefined;
  const sessionsQ = useSessions(
    { dir: directory, sinceHours },
    { refetchInterval: 5000, enabled: !!directory },
  );
  const sessions = sessionsQ.data ?? [];
  const sessionsLoaded = !sessionsQ.isLoading;

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

  const handleTmuxSwitchRef = useRef(handleTmuxSwitch);
  useEffect(() => { handleTmuxSwitchRef.current = handleTmuxSwitch; }, [handleTmuxSwitch]);
  const handleOpenVSCodeRef = useRef(handleOpenVSCode);
  useEffect(() => { handleOpenVSCodeRef.current = handleOpenVSCode; }, [handleOpenVSCode]);
  const matchingTmuxSessionRef = useRef(matchingTmuxSession);
  useEffect(() => { matchingTmuxSessionRef.current = matchingTmuxSession; }, [matchingTmuxSession]);
  const directoryRef = useRef(directory);
  useEffect(() => { directoryRef.current = directory; }, [directory]);

  const switchTmuxShortcut = useMemo(() => ({
    id: 'project.switch-tmux',
    scope: 'project' as const,
    keys: { code: 'KeyT', alt: true },
    description: 'Switch tmux for current project',
    enabled: () => !!matchingTmuxSessionRef.current,
    handler: () => handleTmuxSwitchRef.current(),
  }), []);

  const openVscodeShortcut = useMemo(() => ({
    id: 'project.open-vscode',
    scope: 'project' as const,
    keys: { code: 'KeyV', alt: true },
    description: 'Open current project in VS Code',
    enabled: () => !!directoryRef.current,
    handler: () => handleOpenVSCodeRef.current(),
  }), []);

  useShortcut(switchTmuxShortcut);
  useShortcut(openVscodeShortcut);

  return (
    <div style={{ flex: 1, overflowY: 'auto', minHeight: 0 }}>
      {pendingTmuxSession && pickerPos && (
        <TmuxClientPicker
          pickerRef={pickerRef}
          pos={pickerPos}
          clients={tmux.clients}
          onSelect={handleClientSelect}
        />
      )}
      <h2 className="section-title" style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <span style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis' }}>{directory}</span>
        {matchingTmuxSession && (
          <button
            type="button"
            className="tmux-switch-btn"
            onClick={(e) => handleTmuxSwitch(e.currentTarget)}
            title={`Switch tmux to ${shortPath(matchingTmuxSession.name)} (T)`}
          >tmux</button>
        )}
        {directory && (
          <button type="button" className="vscode-btn" onClick={handleOpenVSCode} title="Open in VS Code (V)">VS Code</button>
        )}
        {directory && worktreeSessionsAllowed && (
          <button
            type="button"
            className="oc-time-range-btn"
            onClick={() => navigate(`/project/${encodeURIComponent(directory)}/worktrees`)}
            title="View project worktrees"
          >
            Worktrees
          </button>
        )}
      </h2>
      <div className="oc-time-range">
        {TIME_RANGE_OPTIONS.map((opt) => (
          <button
            key={opt.value}
            className={`oc-time-range-btn${timeRange === opt.value ? ' active' : ''}`}
            onClick={() => setTimeRange(opt.value)}
          >{opt.label}</button>
        ))}
        <button
          className={`oc-time-range-btn${excludeArchived ? ' active' : ''}`}
          onClick={() => setExcludeArchived(!excludeArchived)}
        >Exclude archived</button>
      </div>
      <SessionTable sessions={sessions} showProject={false} loading={!sessionsLoaded} includeArchived={!excludeArchived} />
    </div>
  );
}
