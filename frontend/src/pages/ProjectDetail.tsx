import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { useParams } from 'react-router-dom';
import type { Session } from '../lib/api';
import { usePageTitle } from '../lib/headerContext';
import { SessionTable } from '../components/SessionTable';
import { useTmux } from '../lib/useTmux';
import { useApiStore } from '../lib/apiStore';
import { shortPath } from '../lib/format';
import { openVSCode } from '../lib/shortcuts';
import { useShortcut } from '../lib/shortcutRegistry';

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
      const nextSessions = await getSessions({ dir: directory });
      // Coerce a nil-slice JSON null into [] so SessionTable / its
      // visibility filter never see null.
      setSessions(nextSessions ?? []);
      setSessionsLoaded(true);
    } catch {
      // error tracked by useApiRequest
    }
  }, [directory, getSessions]);

  useEffect(() => {
    let cancelled = false;

    async function loadProject() {
      if (!directory) {
        if (!cancelled) setSessions([]);
        return;
      }

      try {
const nextSessions = await getSessions({ dir: directory, since: Date.now() - 12 * 60 * 60 * 1000 });
        if (cancelled) return;
        // See above: guard against JSON-null from /api/sessions.
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
  }, [directory, getSessions]);

  // Auto-refresh every 5 seconds
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
      <SessionTable sessions={sessions} showProject={false} loading={!sessionsLoaded} includeArchived />
    </div>
  );
}
