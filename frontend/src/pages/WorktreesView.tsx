import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import type { WorktreeEntry } from '../lib/api';
import { api } from '../lib/api';
import { useApiStore } from '../lib/apiStore';
import { usePageTitle } from '../lib/headerContext';
import { openVSCode } from '../lib/shortcuts';
import { relativeTime, shortPath } from '../lib/format';
import { useUiStore } from '../lib/uiStore';
import { useOpencodeLaunch } from '../lib/useCapabilities';
import { sessionsForWorktree } from '../lib/worktrees';
import { WorktreesTableSkeleton } from '../components/Skeleton';
import { ProjectLabel } from '../components/ProjectLabel';
import { DataTable } from '../components/DataTable';
import { RefreshButton } from '../components/RefreshButton';
import { Button, ButtonGroup, RouteButton } from '../components/Control';
import { HeaderPortal } from './session-detail/MobileHeaderControls';
import styles from './WorktreesView.module.css';

export function WorktreesView() {
  const { dir } = useParams();
  const projectDir = dir ? decodeURIComponent(dir) : '';
  // The owning machine travels in `?remoteId=` (absent = this machine) and
  // is sent explicitly on every request, so an identical path on another
  // host can never be listed, deleted, or launched into by inference.
  const [searchParams] = useSearchParams();
  const remoteId = searchParams.get('remoteId') || 'local';
  // Keyed by (owner, project): rows, in-flight responses, and delete /
  // force-delete consent all belong to one machine's project, so switching
  // either remounts with fresh state instead of carrying them across.
  return <WorktreesContent key={`${remoteId}\n${projectDir}`} projectDir={projectDir} remoteId={remoteId} />;
}

function WorktreesContent({ projectDir, remoteId }: { projectDir: string; remoteId: string }) {
  usePageTitle(projectDir ? `${shortPath(projectDir)} · Worktrees` : 'Worktrees');
  const ownerQuery = remoteId === 'local' ? '' : `?remoteId=${encodeURIComponent(remoteId)}`;

  const navigate = useNavigate();
  const allowed = useOpencodeLaunch(remoteId);
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
        api.worktree.list(projectDir, remoteId),
        refreshCachedSessions().catch(() => []),
      ]);
      setWorktrees(wtResp.worktrees);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [projectDir, remoteId, refreshCachedSessions]);

  // Only list once the owner can serve it: a disconnected owner would 503,
  // and when capability polling reports it back the rows load by themselves.
  useEffect(() => {
    if (allowed) void load();
  }, [allowed, load]);

  const remove = useCallback(
    async (wt: WorktreeEntry, force: boolean) => {
      setRemoving(wt.path);
      setError(null);
      try {
        await api.worktree.remove({ projectDir, path: wt.path, force, remoteId });
        setConfirmPath(null);
        setDirtyPath(null);
        await load();
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        // A dirty worktree comes back as 409; offer a force retry inline
        // rather than surfacing it as a generic error. 409 on this route
        // only ever means a git-level conflict — a disconnected remote
        // owner is 503 `remote_not_connected` — so matching the prose is
        // unambiguous.
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
    [projectDir, remoteId, load],
  );

  const rows = useMemo(
    () =>
      worktrees.map((wt) => {
        const stats = sessionsForWorktree(wt, cachedSessions, remoteId);
        return { wt, stats };
      }),
    [worktrees, cachedSessions, remoteId],
  );

  if (!allowed) {
    return (
      <div>
        <div className="oc-list-error">Worktree sessions are unavailable on this host.</div>
      </div>
    );
  }

  return (
    <div>
      <HeaderPortal>
        <ButtonGroup label="Worktree actions">
          <RouteButton size="small" to={`/project/${encodeURIComponent(projectDir)}${ownerQuery}`}>
            Back to project
          </RouteButton>
          <RefreshButton size="small" variant="default" onClick={() => void load()} loading={loading} />
          <Button size="small" variant="accent"
            type="button"
            onClick={() => openWorktreeForm({ projectDir, remoteId })}
          >
            New worktree session
          </Button>
        </ButtonGroup>
      </HeaderPortal>

      {loading ? (
        <WorktreesTableSkeleton rows={3} />
      ) : error ? (
        <div className="oc-list-error">{error}</div>
      ) : (
        <DataTable framed className={styles.table}>
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
                <td colSpan={5} className={styles.empty}>
                  No worktrees found
                </td>
              </tr>
            ) : (
              rows.map(({ wt, stats }) => (
                <tr key={wt.path}>
                  <td>
                    <div className={styles.branch}>
                      <span>{wt.branch || '(detached)'}</span>
                      {wt.main && <span className={styles.chip}>main</span>}
                      {wt.locked && <span className={styles.chip}>locked</span>}
                    </div>
                  </td>
                  <td>
                    <div className={styles.path}>
                      <ProjectLabel path={wt.path} className={styles.project} />
                    </div>
                  </td>
                  <td title={stats.sessions.map((s) => s.id).join(', ')}>
                    {stats.sessions.length}
                  </td>
                  <td>{stats.lastActivity ? relativeTime(stats.lastActivity) : '—'}</td>
                  <td>
                    <ButtonGroup label={`Actions for ${wt.branch || 'detached worktree'}`} className={styles.actions}>
                      <Button size="small"
                        type="button"
                        title="Open in VS Code"
                        onClick={() => openVSCode(wt.path)}
                      >
                        VS Code
                      </Button>
                      <Button size="small"
                        type="button"
                        disabled={stats.sessions.length === 0}
                        onClick={() => {
                          if (stats.sessions.length === 0) return;
                          const newest = [...stats.sessions].sort((a, b) => b.timeUpdated - a.timeUpdated)[0];
                          navigate(`/session/${encodeURIComponent(newest.id)}?platform=${encodeURIComponent(newest.platform)}`);
                        }}
                      >
                        Open session
                      </Button>
                      {!wt.main &&
                        (dirtyPath === wt.path ? (
                          <Button size="small" variant="danger"
                            type="button"
                            disabled={removing === wt.path}
                            title="Worktree has uncommitted changes — discard them and delete"
                            onClick={() => void remove(wt, true)}
                          >
                            Force delete
                          </Button>
                        ) : confirmPath === wt.path ? (
                          <Button size="small" variant="danger" aria-busy={removing === wt.path}
                            type="button"
                            disabled={removing === wt.path}
                            onClick={() => void remove(wt, false)}
                          >
                            {removing === wt.path ? 'Deleting…' : 'Confirm delete'}
                          </Button>
                        ) : (
                          <Button size="small" variant="danger"
                            type="button"
                            onClick={() => {
                              setError(null);
                              setDirtyPath(null);
                              setConfirmPath(wt.path);
                            }}
                          >
                            Delete
                          </Button>
                        ))}
                    </ButtonGroup>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </DataTable>
      )}
    </div>
  );
}
