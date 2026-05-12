import { useEffect, useMemo, useState } from 'react';
import type { WorkingTreeFile } from '../lib/api';
import { useWorkingTreeDiff } from '../lib/useWorkingTreeDiff';
import { useInfiniteRows } from '../lib/useInfiniteRows';
import { RawDiffView } from './RawDiffView';
import { ChangesRefreshButton, type PaneSummary } from './SessionChangesSidebar';
import { groupWorkingTreeFiles } from './groupWorkingTreeFiles';
import { SidebarFileListSkeleton } from './Skeleton';

// Lazy-mount budget for the per-file rows. Working trees with
// hundreds of dirty files (post-rebase, after a generated-files
// dump, etc.) only paint the first chunk; the rest stream in as
// the sentinel enters the viewport. Same pattern as the session-
// changes sidebar.
const INITIAL_FILE_GROUPS = 20;
const FILE_GROUPS_CHUNK = 20;

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

interface WorkingTreeChangesSidebarProps {
  // Absolute path to the session's directory. Empty string disables
  // the hook and renders an empty state.
  directory: string | undefined;
  dirtyTick?: number;
  // When true the sidebar's outer chrome is omitted; used by the
  // split mode in RightPanel where the parent supplies a header.
  embedded?: boolean;
  // Called whenever the underlying data updates. Used by RightPanel
  // to render the "N files +A -D" summary in the pane header.
  onSummaryChange?: (summary: PaneSummary) => void;
  // Called once with a stable refresh callback so embedded parents
  // (RightPanel) can render their own refresh button in the pane
  // header. Mirrors SessionChangesSidebar.onRefresh.
  onRefresh?: (refresh: () => void) => void;
  // Called whenever the underlying request's loading flag flips.
  // Mirrors SessionChangesSidebar.onLoadingChange.
  onLoadingChange?: (loading: boolean) => void;
}

const STATUS_LABELS: Record<WorkingTreeFile['status'], string> = {
  modified: 'M',
  added: 'A',
  deleted: 'D',
  renamed: 'R',
  untracked: '?',
};

export function WorkingTreeChangesSidebar({ directory, dirtyTick, embedded = false, onSummaryChange, onRefresh, onLoadingChange }: WorkingTreeChangesSidebarProps) {
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

  // Surface counts to the parent for header rendering. Same contract
  // as SessionChangesSidebar, so RightPanel can treat both panes
  // uniformly.
  useEffect(() => {
    if (!onSummaryChange) return;
    onSummaryChange({
      files: totals.files,
      additions: totals.add,
      deletions: totals.del,
    });
  }, [totals.files, totals.add, totals.del, onSummaryChange]);

  // Forward refresh upward so RightPanel can render its own button
  // in the embedded pane header. Mirrors SessionChangesSidebar.
  useEffect(() => {
    if (!onRefresh) return;
    onRefresh(refresh);
  }, [refresh, onRefresh]);

  // Forward loading state so RightPanel can spin its refresh button
  // while a working-tree fetch is in flight.
  useEffect(() => {
    if (!onLoadingChange) return;
    onLoadingChange(loading);
  }, [loading, onLoadingChange]);

  // Lazy-mount budget over the flat file list. We then group the
  // *visible* slice so the section headers and counts adjust as
  // more rows stream in. Counts in the section header reflect what
  // is currently mounted, which matches the user's mental model:
  // "I see N rows under Changed".
  const {
    visibleCount: visibleFileCount,
    sentinelRef: fileSentinelRef,
    hasMore: hasMoreFiles,
  } = useInfiniteRows({
    total: files.length,
    initial: INITIAL_FILE_GROUPS,
    chunkSize: FILE_GROUPS_CHUNK,
  });

  const visibleGroups = useMemo(
    () => groupWorkingTreeFiles(files.slice(0, visibleFileCount)),
    [files, visibleFileCount],
  );

  const Body = (
    <div className="oc-changes-sidebar-body oc-changes-list-body">
      {!enabled && (
        <div className="oc-changes-sidebar-empty">No directory available.</div>
      )}
      {enabled && loading && !data && (
        <SidebarFileListSkeleton rows={6} />
      )}
      {enabled && notRepo && (
        <div className="oc-changes-sidebar-empty">
          This project is not a git repository.
        </div>
      )}
      {enabled && error && (
        <div className="oc-changes-sidebar-error">Failed to load diff: {error}</div>
      )}
      {enabled && data && files.length === 0 && !loading && !notRepo && !error && (
        <div className="oc-changes-sidebar-empty">
          Working tree is clean.
        </div>
      )}
      {enabled && data && files.length > 0 && (
        <>
          {data.truncated && (
            <div className="oc-changes-sidebar-error">
              Diff is large — some files were omitted. Use your editor to view the full diff.
            </div>
          )}
          {visibleGroups.map((g) => (
            <section key={g.id} className={`oc-changes-list-section oc-changes-list-section-${g.id}`}>
              <header className="oc-changes-list-section-header">
                <span className="oc-changes-list-section-label">{g.label}</span>
                <span className="oc-changes-list-section-count">({g.files.length})</span>
              </header>
              <ul className="oc-changes-list">
                {g.files.map((f) => (
                  <WorkingTreeFileRow key={f.path} file={f} />
                ))}
              </ul>
            </section>
          ))}
          {hasMoreFiles && (
            <div
              ref={fileSentinelRef}
              className="oc-diff-sentinel"
              aria-hidden="true"
            />
          )}
        </>
      )}
    </div>
  );

  if (embedded) {
    // The parent owns the chrome (header/title) in split mode;
    // we only render the body and the branch summary inline at
    // the top.
    return (
      <div className="oc-changes-sidebar-embedded">
        {data && data.branch && (
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
        {Body}
      </div>
    );
  }

  return (
    <aside className="oc-changes-sidebar" aria-label="Working tree changes">
      <div className="oc-changes-sidebar-header">
        <span className="oc-changes-sidebar-title">
          {totals.files > 0
            ? `${totals.files} ${totals.files === 1 ? 'file' : 'files'} in working tree`
            : 'Working tree'}
        </span>
        <span className="oc-changes-sidebar-counts">
          {totals.files > 0 && (
            <>
              <span className="oc-changes-add">+{totals.add}</span>
              <span className="oc-changes-del">-{totals.del}</span>
            </>
          )}
        </span>
        <ChangesRefreshButton onClick={refresh} loading={loading} disabled={!enabled} />
      </div>
      {data && data.branch && (
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
      {Body}
    </aside>
  );
}

interface WorkingTreeFileRowProps {
  file: WorkingTreeFile;
}

// One file row in the working-tree list. Renders as a single line
// (status badge + path + +A/-D counts); clicking the row toggles
// an inline diff body underneath. Each row owns its own collapse
// state, so multiple rows can be open at the same time.
//
// Default state is collapsed — opening a session shouldn't dump
// every diff at once. Matches the screenshot reference where the
// list reads like `git status -s` until you ask for more.
function WorkingTreeFileRow({ file }: WorkingTreeFileRowProps) {
  const [expanded, setExpanded] = useState(false);
  return (
    <li>
      <button
        type="button"
        className="oc-changes-list-row"
        onClick={() => setExpanded((e) => !e)}
        aria-expanded={expanded}
      >
        <span
          className={`oc-change-group-status oc-change-group-status-${file.status}`}
          title={file.status}
        >
          {STATUS_LABELS[file.status]}
        </span>
        <span className="oc-changes-list-path" title={file.path}>
          {file.oldPath && file.status === 'renamed' ? `${file.oldPath} → ${file.path}` : file.path}
        </span>
        <span className="oc-changes-list-counts">
          {file.additions > 0 && <span className="oc-changes-add">+{file.additions}</span>}
          {file.deletions > 0 && <span className="oc-changes-del">-{file.deletions}</span>}
        </span>
      </button>
      {expanded && (
        <div className="oc-changes-list-body-expanded">
          {file.isBinary
            ? <div className="oc-diff-empty">Binary file — diff not shown.</div>
            : <RawDiffView diff={file.diff} filePath={file.path} />}
        </div>
      )}
    </li>
  );
}
