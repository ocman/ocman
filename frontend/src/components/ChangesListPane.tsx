import { useLayoutEffect } from 'react';
import type { ReactNode } from 'react';
import './SessionChangesSidebar.css';
import { useInfiniteRows } from '../lib/useInfiniteRows';
import { useSidebarCallbacks } from '../lib/useSidebarCallbacks';
import type { FullscreenDiffFile } from './DiffFullscreenModal';
import { useFullscreenDiff } from './useFullscreenDiff';
import type { PaneSummary } from './SessionChangesSidebar';
import { SkeletonLines } from './Skeleton';

// Lazy-mount budget for the per-file rows. Change sets with hundreds
// of files only paint the first chunk on first paint; the rest
// stream in as the sentinel below the last row enters view.
const INITIAL_FILE_GROUPS = 20;
const FILE_GROUPS_CHUNK = 20;

// Callbacks a changes pane forwards to its parent (RightPanel).
export interface ChangesPaneCallbacks {
  // Called whenever the underlying data updates, to render the
  // "N files +A -D" summary in the pane header.
  onSummaryChange?: (summary: PaneSummary) => void;
  // Called once with a stable refresh callback so the parent can
  // render its own refresh button in the pane header.
  onRefresh?: (refresh: () => void) => void;
  // Called whenever the underlying request's loading flag flips.
  onLoadingChange?: (loading: boolean) => void;
  // Called once with a stable callback that opens the fullscreen
  // diff browser, so the parent can put the button in its header.
  onFullscreen?: (open: () => void) => void;
}

interface ChangesListPaneProps<F> extends ChangesPaneCallbacks {
  enabled: boolean;
  hasData: boolean;
  loading: boolean;
  error: string | null;
  refresh: () => void;
  // Files rendered in the list; empty when the list must not show.
  files: F[];
  summary: PaneSummary;
  fullscreenTitle: string;
  fullscreenFiles: FullscreenDiffFile[];
  // Prefix for an initial-load failure, e.g. "Failed to load diff".
  loadErrorLabel: string;
  // Shown when loaded with no files; null suppresses the empty state.
  emptyMessage: ReactNode;
  // Rendered above the scrolling body (e.g. the branch summary).
  header?: ReactNode;
  // Status message rendered after the loading skeleton.
  notice?: ReactNode;
  renderRows: (visible: F[], openFile: (key: string) => void) => ReactNode;
}

// ChangesListPane is the shared shell of the session-changes and
// working-tree panes: summary/refresh/loading forwarding, the
// fullscreen diff browser, loading/error/empty states and the
// lazily-mounted file list.
export function ChangesListPane<F>({
  enabled,
  hasData,
  loading,
  error,
  refresh,
  files,
  summary,
  fullscreenTitle,
  fullscreenFiles,
  loadErrorLabel,
  emptyMessage,
  header,
  notice,
  renderRows,
  onSummaryChange,
  onRefresh,
  onLoadingChange,
  onFullscreen,
}: ChangesListPaneProps<F>) {
  const { files: fileCount, additions, deletions } = summary;
  useLayoutEffect(() => {
    if (!onSummaryChange) return;
    onSummaryChange({ files: fileCount, additions, deletions });
  }, [fileCount, additions, deletions, onSummaryChange]);

  useSidebarCallbacks({ refresh, loading, onRefresh, onLoadingChange });

  const { visibleCount, sentinelRef, hasMore } = useInfiniteRows({
    total: files.length,
    initial: INITIAL_FILE_GROUPS,
    chunkSize: FILE_GROUPS_CHUNK,
  });

  // The pane owns the modal; the parent only gets a callback to open it.
  const { openFile, modal: Fullscreen } = useFullscreenDiff(fullscreenTitle, fullscreenFiles, onFullscreen);

  return (
    <div className="oc-changes-sidebar-embedded">
      {header}
      <div className="oc-changes-sidebar-body oc-changes-list-body">
        {enabled && loading && !hasData && (
          <SkeletonLines label="Loading file list" />
        )}
        {notice}
        {enabled && error && (
          <div className="oc-changes-sidebar-error" role="alert">
            {hasData
              ? <>Refresh failed: {error}. Showing previous results; data may be out of date.</>
              : <>{loadErrorLabel}: {error}</>}
          </div>
        )}
        {enabled && hasData && files.length === 0 && !loading && emptyMessage != null && (
          <div className="oc-changes-sidebar-empty">{emptyMessage}</div>
        )}
        {enabled && hasData && files.length > 0 && (
          <>
            {renderRows(files.slice(0, visibleCount), openFile)}
            {hasMore && (
              <div
                ref={sentinelRef}
                className="oc-diff-sentinel"
                aria-hidden="true"
              />
            )}
          </>
        )}
      </div>
      {Fullscreen}
    </div>
  );
}
