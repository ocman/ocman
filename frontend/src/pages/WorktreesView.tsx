import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import type { WorktreeEntry } from '../lib/api';
import { api } from '../lib/api';
import { useApiStore } from '../lib/apiStore';
import { usePageTitle } from '../lib/headerContext';
import { openVSCode } from '../lib/shortcuts';
import { relativeTime, shortPath } from '../lib/format';
import { useUiStore } from '../lib/uiStore';
import { useWorktreeSessions } from '../lib/useCapabilities';
import { sessionsForWorktree } from '../lib/worktrees';
import { WorktreesTableSkeleton } from '../components/Skeleton';
import './Dashboard.css';
import './WorktreesView.css';

export function WorktreesView() {
  const { dir } = useParams();
  const projectDir = dir ? decodeURIComponent(dir) : '';
  usePageTitle(projectDir ? `${shortPath(projectDir)} · Worktrees` : 'Worktrees');

  const navigate = useNavigate();
  const allowed = useWorktreeSessions();
  const openWorktreeForm = useUiStore((s) => s.openWorktreeForm);
  const cachedSessions = useApiStore((s) => s.cachedSessions);
  const refreshCachedSessions = useApiStore((s) => s.refreshCachedSessions);

  const [worktrees, setWorktrees] = useState<WorktreeEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // Per-row removal UI state keyed by worktree path. `confirm` arms the
  // two-step delete; `dirty` means the backend refused (409) because the
  // tree has uncommitted changes — the button switches to "Force delete".
  const [removing, setRemoving] = useState<string | null>(null);
  const [confirmPath, setConfirmPath] = useState<string | null>(null);
  const [dirtyPath, setDirtyPath] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!projectDir) {
      setWorktrees([]);
      setLoading(false);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const [wtResp] = await Promise.all([
        api.worktree.list(projectDir),
        refreshCachedSessions().catch(() => []),
      ]);
      setWorktrees(wtResp.worktrees);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [projectDir, refreshCachedSessions]);

  useEffect(() => {
    void load();
  }, [load]);

  const remove = useCallback(
    async (wt: WorktreeEntry, force: boolean) => {
      setRemoving(wt.path);
      setError(null);
      try {
        await api.worktree.remove({ projectDir: projectDir, path: wt.path, force });
        setConfirmPath(null);
        setDirtyPath(null);
        await load();
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        // A dirty worktree comes back as 409; offer a force retry inline
        // rather than surfacing it as a generic error.
        if (/uncommitted changes/i.test(msg)) {
          setDirtyPath(wt.path);
        } else {
          setError(msg);
          setConfirmPath(null);
        }
      } finally {
        setRemoving(null);
      }
    },
    [projectDir, load],
  );

  const rows = useMemo(
    () =>
      worktrees.map((wt) => {
        const stats = sessionsForWorktree(wt, cachedSessions);
        return { wt, stats };
      }),
    [worktrees, cachedSessions],
  );

  if (!allowed) {
    return (
      <div className="metrics-page oc-worktrees-page">
        <div className="oc-list-error">Worktree sessions are unavailable on this host.</div>
      </div>
    );
  }

  return (
    <div className="metrics-page oc-worktrees-page">
      <div className="oc-worktrees-header">
        <div>
          <h2 className="section-title">Worktrees</h2>
          <div className="mono oc-worktrees-project">{projectDir}</div>
        </div>
        <div className="oc-worktrees-actions">
          <Link className="oc-time-range-btn" to={`/project/${encodeURIComponent(projectDir)}`}>
            Back to project
          </Link>
          <button className="oc-time-range-btn" type="button" onClick={() => void load()}>
            Refresh
          </button>
          <button
            className="oc-time-range-btn active"
            type="button"
            onClick={() => openWorktreeForm({ projectDir })}
          >
            New worktree session
          </button>
        </div>
      </div>

      {loading ? (
        <WorktreesTableSkeleton rows={3} />
      ) : error ? (
        <div className="oc-list-error">{error}</div>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Branch</th>
              <th>Path</th>
              <th>Sessions</th>
              <th>Last activity</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 ? (
              <tr>
                <td colSpan={5} className="oc-worktrees-empty">
                  No worktrees found
                </td>
              </tr>
            ) : (
              rows.map(({ wt, stats }) => (
                <tr key={wt.path}>
                  <td>
                    <div className="oc-worktrees-branch">
                      <span>{wt.branch || '(detached)'}</span>
                      {wt.main && <span className="oc-worktrees-chip">main</span>}
                      {wt.locked && <span className="oc-worktrees-chip">locked</span>}
                    </div>
                  </td>
                  <td>
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                      <span style={{ color: 'var(--accent)', fontWeight: 500 }}>{shortPath(wt.path)}</span>
                      <span className="mono">{wt.path}</span>
                    </div>
                  </td>
                  <td title={stats.sessions.map((s) => s.id).join(', ')}>
                    {stats.sessions.length}
                  </td>
                  <td>{stats.lastActivity ? relativeTime(stats.lastActivity) : '—'}</td>
                  <td>
                    <div className="oc-worktrees-row-actions">
                      <button
                        type="button"
                        className="vscode-btn"
                        title="Open in VS Code"
                        onClick={() => openVSCode(wt.path)}
                      >
                        VS Code
                      </button>
                      <button
                        type="button"
                        className="oc-time-range-btn"
                        disabled={stats.sessions.length === 0}
                        onClick={() => {
                          if (stats.sessions.length === 0) return;
                          const newest = [...stats.sessions].sort((a, b) => b.timeUpdated - a.timeUpdated)[0];
                          navigate(`/session/${newest.id}`);
                        }}
                      >
                        Open session
                      </button>
                      {!wt.main &&
                        (dirtyPath === wt.path ? (
                          <button
                            type="button"
                            className="oc-time-range-btn oc-worktree-delete-force"
                            disabled={removing === wt.path}
                            title="Worktree has uncommitted changes — discard them and delete"
                            onClick={() => void remove(wt, true)}
                          >
                            Force delete
                          </button>
                        ) : confirmPath === wt.path ? (
                          <button
                            type="button"
                            className="oc-time-range-btn oc-worktree-delete-confirm"
                            disabled={removing === wt.path}
                            onClick={() => void remove(wt, false)}
                          >
                            {removing === wt.path ? 'Deleting…' : 'Confirm delete'}
                          </button>
                        ) : (
                          <button
                            type="button"
                            className="oc-time-range-btn"
                            onClick={() => {
                              setError(null);
                              setDirtyPath(null);
                              setConfirmPath(wt.path);
                            }}
                          >
                            Delete
                          </button>
                        ))}
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      )}
    </div>
  );
}
