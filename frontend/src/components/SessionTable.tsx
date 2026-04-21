import { useState, useRef, useEffect } from 'react';
import './SessionTable.css';
import { useNavigate } from 'react-router-dom';
import type { Session, GitInfo } from '../lib/api';
import { useApiStore } from '../lib/apiStore';
import { cleanTitle, formatDuration, relativeTime, shortPath } from '../lib/format';
import { StatusBadge } from './StatusBadge';
import { PlatformBadge } from './PlatformBadge';
import type { TmuxState } from '../lib/useTmux';
import { filterVisibleSessions } from '../lib/sessionVisibility';

export function ShortPath({ path }: { path: string }) {
  const parts = (path || '').split('/').filter(Boolean);
  const last = parts.pop() || '';
  const prefix = parts.length > 0 ? parts.slice(-1).join('/') + '/' : '';
  return <><span className="short-path-prefix">{prefix}</span><span className="short-path-last">{last}</span></>;
}

/**
 * Tiny git status indicator rendered under the project path: branch
 * name, optional ahead/behind counts (when upstream tracking knows
 * about them), and a trailing `*` when the working tree is dirty.
 *
 * Every field carries its own `title` so hovering the specific glyph
 * explains what it means — handier than a single combined tooltip on
 * the whole line, especially when most of them are empty.
 *
 * Returns null when no git info is available so callers can just
 * render it unconditionally.
 */
export function GitStatusLine({ info }: { info?: GitInfo | null }) {
  if (!info || !info.branch) return null;
  const dirtyCls = info.dirty ? ' git-status-dirty' : '';
  return (
    <div className={`git-status${dirtyCls}`}>
      <span className="git-status-branch" title={`Current branch: ${info.branch}`}>
        {info.branch}
      </span>
      {info.ahead > 0 && (
        <span
          className="git-status-ahead"
          title={`${info.ahead} commit${info.ahead === 1 ? '' : 's'} ahead of upstream (not yet pushed)`}
        >&uarr;{info.ahead}</span>
      )}
      {info.behind > 0 && (
        <span
          className="git-status-behind"
          title={`${info.behind} commit${info.behind === 1 ? '' : 's'} behind upstream (not yet pulled)`}
        >&darr;{info.behind}</span>
      )}
      {info.dirty && (
        <span
          className="git-status-mark"
          title="Working tree has uncommitted changes"
        >*</span>
      )}
    </div>
  );
}

interface Props {
  sessions: Session[];
  showProject?: boolean;
  loading?: boolean;
  tmux?: TmuxState;
  includeArchived?: boolean;
}

function ArchiveIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M2 3.5h12v2H2zm1 3h10v6H3zm3 2.5h4" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function SessionTable({ sessions, showProject, loading, tmux, includeArchived }: Props) {
  const navigate = useNavigate();
  const archiveSession = useApiStore((state) => state.archiveSession);
  const [pickerFor, setPickerFor] = useState<string | null>(null);
  const [pickerPos, setPickerPos] = useState<{ top: number; left: number } | null>(null);
  const [archivingSessionIds, setArchivingSessionIds] = useState<Set<string>>(new Set());
  const [locallyArchivedSessionIds, setLocallyArchivedSessionIds] = useState<Set<string>>(new Set());
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

  const handleArchiveSession = async (e: React.MouseEvent, session: Session) => {
    e.stopPropagation();
    if (archivingSessionIds.has(session.id)) return;

    setArchivingSessionIds(prev => new Set(prev).add(session.id));
    try {
      await archiveSession(session.platform, session.id, session.timeUpdated, true);
      setLocallyArchivedSessionIds(prev => new Set(prev).add(session.id));
    } catch (err) {
      console.error('Failed to archive session', err);
    } finally {
      setArchivingSessionIds(prev => {
        const next = new Set(prev);
        next.delete(session.id);
        return next;
      });
    }
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
  const visibleSessions = (includeArchived ? sessions : filterVisibleSessions(sessions))
    .filter(session => includeArchived || !locallyArchivedSessionIds.has(session.id));

  if (!visibleSessions.length) {
    return (
      <table>
        <tbody>
          <tr>
            <td colSpan={colCount} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: 24 }}>
              {includeArchived ? 'No sessions found' : 'No active sessions found'}
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
            {showProject && <th>Project</th>}
            <th>Activity</th>
            <th>Started</th>
            <th style={{ width: 44 }} />
          </tr>
        </thead>
        <tbody>
          {visibleSessions.map(s => {
            const hasTmux = tmux?.available && tmux.findSession(s.directory);
            const seenLatest = (s.status === 'waiting' || s.status === 'error') && s.seen;
            const pending = s.pendingPermission || s.pendingQuestion;
            return (
              <tr
                key={s.id}
                className={s.liveConnection ? '' : 'no-port'}
                onClick={() => navigate(`/session/${s.id}`)}
              >
                <td>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    <StatusBadge status={s.status} compact seen={seenLatest} pending={pending} />
                    <span style={{ color: 'var(--accent)', fontWeight: 500 }}>{cleanTitle(s.title) || 'Untitled'}</span>
                  </div>
                  <div className="mono">
                    <PlatformBadge platform={s.platform} variant="plain" />
                    {' '}
                    {s.id}
                    <span className="session-row-actions">
                      {!showProject && hasTmux && (
                        <button
                          className="tmux-switch-btn"
                          onClick={(e) => handleTmuxSwitch(e, s.directory)}
                          title={`Switch tmux to ${shortPath(s.directory)}`}
                        >tmux</button>
                      )}
                      {!showProject && (
                        <button
                          className="tmux-switch-btn"
                          onClick={(e) => { e.stopPropagation(); window.location.href = `vscode://file${s.directory}`; }}
                          title="Open in VS Code"
                        >&lt;/&gt;</button>
                      )}
                    </span>
                  </div>
                </td>
                {showProject && (
                   <td className="mono">
                    <ShortPath path={s.directory} />
                    <GitStatusLine info={s.gitInfo} />
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
                <td><span title={new Date(s.timeCreated).toLocaleString()}>{relativeTime(s.timeCreated)}</span></td>
                <td className="session-action-cell">
                  <button
                    className="session-archive-btn"
                    onClick={(e) => handleArchiveSession(e, s)}
                    title="Archive session (reappears on new activity)"
                    aria-label="Archive session"
                    disabled={archivingSessionIds.has(s.id)}
                  >
                    <ArchiveIcon />
                  </button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </>
  );
}
