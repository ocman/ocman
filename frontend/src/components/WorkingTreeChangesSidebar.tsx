import { useMemo } from 'react';
import type { WorkingTreeFile } from '../lib/api';
import { useWorkingTreeDiff } from '../lib/useWorkingTreeDiff';
import { RawDiffView } from './RawDiffView';
import type { FullscreenDiffFile } from './DiffFullscreenModal';
import { groupWorkingTreeFiles } from './groupWorkingTreeFiles';
import { ChangedFileRow } from './ChangedFileRow';
import { ChangesListPane, type ChangesPaneCallbacks } from './ChangesListPane';

// WorkingTreeChangesSidebar shows `git diff HEAD` (plus untracked
// files) for the session's directory. Visually mirrors
// SessionChangesSidebar — same header layout, same per-file group
// shape — but the data source is the local working tree, not the
// session's tool calls.
//
// Files are listed under section headers ("Changed (N)",
// "Untracked (N)") and rendered as one-line entries by default;
// clicking a row reveals its diff inline. Multiple rows can be
// expanded at once.
//
// Refreshes via the same dirtyTick prop that drives
// useSessionChanges, so an SSE edit event refreshes both views in
// lockstep.

interface WorkingTreeChangesSidebarProps extends ChangesPaneCallbacks {
  // Absolute path to the session's directory. Empty string disables
  // the hook and renders an empty state.
  directory: string | undefined;
  dirtyTick?: number;
}

const STATUS_LABELS: Record<WorkingTreeFile['status'], string> = {
  modified: 'M',
  added: 'A',
  deleted: 'D',
  renamed: 'R',
  untracked: '?',
};

export function WorkingTreeChangesSidebar({ directory, dirtyTick, ...callbacks }: WorkingTreeChangesSidebarProps) {
  const enabled = !!directory;
  const { data, loading, error, notRepo, refresh } = useWorkingTreeDiff(directory, { enabled, dirtyTick });

  // Defensive default: a faulty backend / older deployment could
  // ship `null` instead of `[]` for files. Coerce here, memoised so
  // the empty-fallback array has a stable identity (otherwise every
  // render builds a fresh `[]` and downstream useMemos thrash).
  const files = useMemo(() => data?.files ?? [], [data]);

  const totals = useMemo(() => {
    let add = 0;
    let del = 0;
    for (const f of files) {
      add += f.additions;
      del += f.deletions;
    }
    return { add, del, files: files.length };
  }, [files]);

  const fullscreenFiles = useMemo(() => toFullscreenFiles(files), [files]);

  return (
    <ChangesListPane
      {...callbacks}
      enabled={enabled}
      hasData={!!data}
      loading={loading}
      error={error}
      refresh={refresh}
      files={files}
      summary={{ files: totals.files, additions: totals.add, deletions: totals.del }}
      fullscreenTitle="Working tree"
      fullscreenFiles={fullscreenFiles}
      loadErrorLabel="Failed to load diff"
      emptyMessage={!notRepo && !error ? 'Working tree is clean.' : null}
      header={data && data.branch && (
        <div className="oc-changes-sidebar-branch">
          <i className="bi bi-git" aria-hidden="true" /> {data.branch}
          {(data.ahead > 0 || data.behind > 0) && (
            <span className="oc-changes-sidebar-branch-ab">
              {data.ahead > 0 && <> ↑{data.ahead}</>}
              {data.behind > 0 && <> ↓{data.behind}</>}
            </span>
          )}
        </div>
      )}
      notice={!enabled ? (
        <div className="oc-changes-sidebar-empty">No directory available.</div>
      ) : notRepo && (
        <div className="oc-changes-sidebar-empty">
          This project is not a git repository.
        </div>
      )}
      // Group only the mounted slice, so section headers and counts
      // reflect what is currently visible: "I see N rows under Changed".
      renderRows={(visible, openFile) => (
        <>
          {data?.truncated && (
            <div className="oc-changes-sidebar-error">
              Diff is large — some files were omitted. Use your editor to view the full diff.
            </div>
          )}
          {groupWorkingTreeFiles(visible).map((g) => (
            <section key={g.id} className={`oc-changes-list-section oc-changes-list-section-${g.id}`}>
              <header className="oc-changes-list-section-header">
                <span className="oc-changes-list-section-label">{g.label}</span>
                <span className="oc-changes-list-section-count">({g.files.length})</span>
              </header>
              <ul className="oc-changes-list">
                {g.files.map((f) => (
                  <WorkingTreeFileRow key={f.path} file={f} onOpen={() => openFile(f.path)} />
                ))}
              </ul>
            </section>
          ))}
        </>
      )}
    />
  );
}

// toFullscreenFiles adapts working-tree files to the fullscreen diff
// browser's file shape, matching the inline rows' rename labels,
// status badges and binary fallback.
function toFullscreenFiles(files: WorkingTreeFile[]): FullscreenDiffFile[] {
  return files.map((f) => ({
    key: f.path,
    path: f.path,
    oldPath: f.oldPath && f.status === 'renamed' ? f.oldPath : undefined,
    label: f.oldPath && f.status === 'renamed' ? `${f.oldPath} → ${f.path}` : f.path,
    status: f.status,
    statusLabel: STATUS_LABELS[f.status],
    additions: f.additions,
    deletions: f.deletions,
    body: f.isBinary
      ? <div className="oc-diff-empty">Binary file — diff not shown.</div>
      : <RawDiffView diff={f.diff} filePath={f.path} />,
  }));
}

interface WorkingTreeFileRowProps {
  file: WorkingTreeFile;
  onOpen: () => void;
}

// One file row in the working-tree list: status badge + path + +A/-D
// counts, reading like `git status -s`. Clicking opens the fullscreen
// diff browser on this file.
function WorkingTreeFileRow({ file, onOpen }: WorkingTreeFileRowProps) {
  return (
    <ChangedFileRow
      path={file.path}
      displayPath={file.oldPath && file.status === 'renamed' ? `${file.oldPath} → ${file.path}` : file.path}
      additions={file.additions}
      deletions={file.deletions}
      onOpen={onOpen}
      statusBadge={
        <span
          className={`oc-change-group-status oc-change-group-status-${file.status}`}
          title={file.status}
        >
          {STATUS_LABELS[file.status]}
        </span>
      }
    />
  );
}
