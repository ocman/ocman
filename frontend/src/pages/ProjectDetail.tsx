import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams } from 'react-router-dom';
import { useHotkeys } from 'react-hotkeys-hook';
import type { Session } from '../lib/api';
import { usePageTitle } from '../lib/headerContext';
import { SessionTable } from '../components/SessionTable';
import { useTmux } from '../lib/useTmux';
import { useApiStore, useApiRequest } from '../lib/apiStore';
import { shortPath } from '../lib/format';
import { openVSCode } from '../lib/shortcuts';

export function ProjectDetail() {
  const { '*': directory } = useParams();
  const projectName = directory?.split('/').pop() || 'Project';
  usePageTitle(projectName);
  const tmux = useTmux();
  const matchingTmuxSession = directory ? tmux.findSession(directory) : undefined;
  const [sessions, setSessions] = useState<Session[]>([]);
  const [pendingTmuxSession, setPendingTmuxSession] = useState<string | null>(null);
  const [pickerPos, setPickerPos] = useState<{ top: number; left: number } | null>(null);
  const pickerRef = useRef<HTMLDivElement>(null);
  const getSessions = useApiStore((state) => state.getSessions);
  const sessionsRequest = useApiRequest(directory ? `sessions:get:dir:${directory}` : 'sessions:get');

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
      setSessions(nextSessions);
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
        const nextSessions = await getSessions({ dir: directory });
        if (cancelled) return;
        setSessions(nextSessions);
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

  useHotkeys('t', (e) => {
    e.preventDefault();
    handleTmuxSwitch();
  }, { enabled: !!matchingTmuxSession, preventDefault: true }, [handleTmuxSwitch, matchingTmuxSession]);

  useHotkeys('v', (e) => {
    e.preventDefault();
    handleOpenVSCode();
  }, { enabled: !!directory, preventDefault: true }, [handleOpenVSCode, directory]);

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
      <SessionTable sessions={sessions} loading={sessionsRequest.loading && sessions.length === 0} tmux={tmux} includeArchived />
    </div>
  );
}
