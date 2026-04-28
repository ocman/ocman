import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import type { Session } from '../lib/api';
import { usePageTitle } from '../lib/headerContext';
import { SessionTable } from '../components/SessionTable';
import { useTmux } from '../lib/useTmux';
import { useApiStore } from '../lib/apiStore';
import { shortPath } from '../lib/format';
import { openVSCode } from '../lib/shortcuts';
import { useShortcut } from '../lib/shortcutRegistry';
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

export function ProjectDetail() {
  const { '*': directory } = useParams();
  const projectName = directory?.split('/').pop() || 'Project';
  usePageTitle(projectName);
  const tmux = useTmux();
  const matchingTmuxSession = directory ? tmux.findSession(directory) : undefined;
  const [sessions, setSessions] = useState<Session[]>([]);
  const [sessionsLoaded, setSessionsLoaded] = useState(false);
  const [pendingTmuxSession, setPendingTmuxSession] = useState<string | null>(null);
  const [pickerPos, setPickerPos] = useState<{ top: number; left: number } | null>(null);
  const pickerRef = useRef<HTMLDivElement>(null);
  const getSessions = useApiStore((state) => state.getSessions);

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

  const load = useCallback(async () => {
    if (!directory) {
      setSessions([]);
      return;
    }

    try {
      const since = timeRange > 0 ? Date.now() - timeRange * 60 * 60 * 1000 : undefined;
      const nextSessions = await getSessions({ dir: directory, since });
      // Coerce a nil-slice JSON null into [] so SessionTable / its
      // visibility filter never see null.
      setSessions(nextSessions ?? []);
      setSessionsLoaded(true);
    } catch {
      // error tracked by useApiRequest
    }
  }, [directory, getSessions, timeRange]);

  // Initial load + reload whenever `directory` or the time-range filter
  // changes. Inlined so the effect body itself doesn't synchronously
  // call a setState-bearing callback (react-hooks/set-state-in-effect).
  useEffect(() => {
    let cancelled = false;

    async function loadProject() {
      if (!directory) {
        if (!cancelled) setSessions([]);
        return;
      }

      try {
        const since = timeRange > 0 ? Date.now() - timeRange * 60 * 60 * 1000 : undefined;
        const nextSessions = await getSessions({ dir: directory, since });
        if (cancelled) return;
        // Coerce a nil-slice JSON null into [] so SessionTable / its
        // visibility filter never see null.
        setSessions(nextSessions ?? []);
        setSessionsLoaded(true);
      } catch {
        // error tracked by useApiRequest
      }
    }

    void loadProject();
    return () => {
      cancelled = true;
    };
  }, [directory, getSessions, timeRange]);

  // Auto-refresh every 5 seconds. `load` is passed to setInterval (not
  // invoked synchronously), so this doesn't trip set-state-in-effect.
  useEffect(() => {
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [load]);

  const handleTmuxSwitch = useCallback((anchor?: HTMLElement | null) => {
    if (!matchingTmuxSession) return;
    if (tmux.isLocal) {
      tmux.switchSession(matchingTmuxSession.name).catch(err => console.error('tmux switch failed', err));
      return;
    }
    if (tmux.clients.length === 1) {
      tmux.switchSession(matchingTmuxSession.name, tmux.clients[0].tty).catch(err => console.error('tmux switch failed', err));
      return;
    }

    const rect = anchor?.getBoundingClientRect();
    setPickerPos(rect ? { top: rect.bottom + 4, left: rect.right } : { top: 88, left: Math.min(window.innerWidth - 24, 420) });
    setPendingTmuxSession(matchingTmuxSession.name);
  }, [matchingTmuxSession, tmux]);

  const handleClientSelect = useCallback((clientTTY: string) => {
    if (!pendingTmuxSession) return;
    tmux.switchSession(pendingTmuxSession, clientTTY).catch(err => console.error('tmux switch failed', err));
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
        <div
          ref={pickerRef}
          className="tmux-client-popover"
          style={{ top: pickerPos.top, left: pickerPos.left }}
        >
          <div className="tmux-client-picker-header">
            <span>Select tmux client</span>
          </div>
          {tmux.clients.map(c => (
            <div
              key={c.tty}
              className="tmux-client-picker-item"
              onClick={() => handleClientSelect(c.tty)}
            >
              <span className="tmux-client-tty">{c.tty}</span>
              <span className="tmux-client-session">{shortPath(c.session)}</span>
              <span className="tmux-client-size">{c.width}&times;{c.height}</span>
            </div>
          ))}
        </div>
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
