import { useState, useRef, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import type { Session } from '../lib/api';
import { formatDuration, relativeTime, shortPath } from '../lib/format';
import { StatusBadge } from './StatusBadge';
import type { TmuxState } from '../lib/useTmux';


interface Props {
  sessions: Session[];
  showProject?: boolean;
  loading?: boolean;
  tmux?: TmuxState;
}

export function SessionTable({ sessions, showProject, loading, tmux }: Props) {
  const navigate = useNavigate();
  const [pickerFor, setPickerFor] = useState<string | null>(null);
  const [pickerPos, setPickerPos] = useState<{ top: number; left: number } | null>(null);
  const pickerRef = useRef<HTMLDivElement>(null);

  // Close picker on outside click
  useEffect(() => {
    if (!pickerFor) return;
    const handle = (e: MouseEvent) => {
      if (pickerRef.current && !pickerRef.current.contains(e.target as Node)) {
        setPickerFor(null);
      }
    };
    document.addEventListener('mousedown', handle);
    return () => document.removeEventListener('mousedown', handle);
  }, [pickerFor]);

  const handleTmuxSwitch = (e: React.MouseEvent, directory: string) => {
    e.stopPropagation();
    if (!tmux) return;
    const ts = tmux.findSession(directory);
    if (!ts) return;

    // Local user: fire directly, server defaults to /dev/ttys000
    if (tmux.isLocal) {
      tmux.switchSession(ts.name).catch(err => console.error('tmux switch failed', err));
      return;
    }

    // Remote user with a single client: fire directly
    if (tmux.clients.length === 1) {
      tmux.switchSession(ts.name, tmux.clients[0].tty).catch(err => console.error('tmux switch failed', err));
      return;
    }

    // Remote user with multiple clients: show floating picker
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    setPickerPos({ top: rect.bottom + 4, left: rect.right });
    setPickerFor(ts.name);
  };

  const handleClientSelect = (clientTTY: string) => {
    if (!tmux || !pickerFor) return;
    tmux.switchSession(pickerFor, clientTTY).catch(err => console.error('tmux switch failed', err));
    setPickerFor(null);
  };

  if (loading) {
    return (
      <div className="oc-list-loading">
        <div className="oc-spinner" />
        Loading sessions...
      </div>
    );
  }

  const colCount = showProject ? 5 : 4;

  if (!sessions.length) {
    return (
      <table>
        <tbody>
          <tr>
            <td colSpan={colCount} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: 24 }}>
              No sessions found
            </td>
          </tr>
        </tbody>
      </table>
    );
  }

  return (
    <>
      {pickerFor && tmux && pickerPos && (
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
      <table>
        <thead>
          <tr>
            <th>Session</th>
            <th>Status</th>
            {showProject && <th>Project</th>}
            <th>Activity</th>
            <th>Started</th>
          </tr>
        </thead>
        <tbody>
          {sessions.map(s => {
            const hasTmux = tmux?.available && tmux.findSession(s.directory);
            return (
              <tr
                key={s.id}
                className={s.hasPort ? '' : 'no-port'}
                onClick={() => navigate(`/session/${s.id}`)}
              >
                <td>
                  <div style={{ color: 'var(--accent)', fontWeight: 500 }}>{s.title || 'Untitled'}</div>
                  <div className="mono">
                    {s.id}
                    {!showProject && (
                      <span className="session-row-actions">
                        {hasTmux && (
                          <button
                            className="tmux-switch-btn"
                            onClick={(e) => handleTmuxSwitch(e, s.directory)}
                            title={`Switch tmux to ${shortPath(s.directory)}`}
                          >tmux</button>
                        )}
                        <button
                          className="tmux-switch-btn"
                          onClick={(e) => { e.stopPropagation(); window.location.href = `vscode://file${s.directory}`; }}
                          title="Open in VS Code"
                        >&lt;/&gt;</button>
                      </span>
                    )}
                  </div>
                </td>
                <td><StatusBadge status={s.status} /></td>
                {showProject && (
                  <td className="mono">
                    {shortPath(s.directory)}
                    <div className="session-row-actions" style={{ marginTop: 2 }}>
                      {hasTmux && (
                        <button
                          className="tmux-switch-btn"
                          onClick={(e) => handleTmuxSwitch(e, s.directory)}
                          title={`Switch tmux to ${shortPath(s.directory)}`}
                        >tmux</button>
                      )}
                      <button
                        className="tmux-switch-btn"
                        onClick={(e) => { e.stopPropagation(); window.location.href = `vscode://file${s.directory}`; }}
                        title="Open in VS Code"
                      >&lt;/&gt;</button>
                    </div>
                  </td>
                )}
                <td className="mono">{s.messageCount} msgs &middot; {formatDuration(s.durationMs)}</td>
                <td>{relativeTime(s.timeCreated)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </>
  );
}
