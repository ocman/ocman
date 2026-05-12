import { useEffect } from 'react';
import './SessionChangesSidebar.css';
import { usePlatformCapabilities } from '../lib/useCapabilities';
import { useSessionChanges } from '../lib/useSessionChanges';
import { useInfiniteRows } from '../lib/useInfiniteRows';
import { FileChangeGroup } from './FileChangeGroup';
import { SidebarFileListSkeleton } from './Skeleton';

// Lazy-mount budget for the per-file rows themselves. Sessions with
// hundreds of touched files need this; we only render the first
// chunk on first paint and stream the rest in as the user scrolls.
// Same useInfiniteRows pattern as the diff renderer. Rows always
// start collapsed — the user expands the ones they care about.
const INITIAL_FILE_GROUPS = 20;
const FILE_GROUPS_CHUNK = 20;

// PaneSummary is the per-tab counts surfaced to the parent so it can
// render "N files +A -D" next to the pane title. Both sidebars emit
// the same shape so RightPanel can render either uniformly.
export interface PaneSummary {
  files: number;
  additions: number;
  deletions: number;
}

interface SessionChangesSidebarProps {
  sessionId: string;
  // Platform ID for the current session — used to look up
  // capability flags. When undefined (loading), the sidebar
  // renders a loading skeleton. AD-12a: never branched on directly.
  platformId: string | undefined;
  // Increments whenever the parent observes a new edit/write part
  // arriving via SSE, prompting a debounced re-fetch. Pass 0 (or
  // omit) for static views (e.g. archived sessions).
  dirtyTick?: number;
  // When true the sidebar's outer chrome is omitted; used by the
  // split mode in RightPanel where the parent supplies a header.
  embedded?: boolean;
  // Called whenever the underlying data updates. Used by RightPanel
  // to render the "N files +A -D" summary in the pane header.
  onSummaryChange?: (summary: PaneSummary) => void;
  // Called once with a stable refresh callback so embedded parents
  // (RightPanel) can render their own refresh button in the pane
  // header. The callback is also used internally by the standalone-
  // mode header. Identity is stable across renders.
  onRefresh?: (refresh: () => void) => void;
  // Called whenever the underlying request's loading flag flips.
  // Lets embedded parents drive a spinner on their refresh button
  // without owning the data hook themselves.
  onLoadingChange?: (loading: boolean) => void;
}

export function SessionChangesSidebar({ sessionId, platformId, dirtyTick, embedded = false, onSummaryChange, onRefresh, onLoadingChange }: SessionChangesSidebarProps) {
  const caps = usePlatformCapabilities(platformId);
  const enabled = caps.fileChanges;
  const { data, loading, error, refresh } = useSessionChanges(sessionId, { enabled, dirtyTick });

  // Defensive defaults: a faulty backend / older deployment could
  // ship `null` instead of `[]` for files, or omit fields entirely.
  // Coerce here so the rest of the component never has to think
  // about it.
  const files = data?.files ?? [];
  const totalAdditions = data?.totalAdditions ?? 0;
  const totalDeletions = data?.totalDeletions ?? 0;
  const filesChanged = data?.filesChanged ?? files.length;

  // Stream additional file groups in as the user scrolls. The hook
  // returns visibleCount=min(initial,total) initially; the sentinel
  // we render below the last group bumps it as it enters view.
  const {
    visibleCount: visibleFileCount,
    sentinelRef: fileSentinelRef,
    hasMore: hasMoreFiles,
  } = useInfiniteRows({
    total: files.length,
    initial: INITIAL_FILE_GROUPS,
    chunkSize: FILE_GROUPS_CHUNK,
  });

  // Forward summary upward whenever the data changes. Only the three
  // numbers we surface — RightPanel is intentionally agnostic about
  // the data shape behind them.
  useEffect(() => {
    if (!onSummaryChange) return;
    onSummaryChange({
      files: filesChanged,
      additions: totalAdditions,
      deletions: totalDeletions,
    });
  }, [filesChanged, totalAdditions, totalDeletions, onSummaryChange]);

  // Forward the refresh callback up to RightPanel so it can wire the
  // embedded pane's "Refresh" button. Only fires when the callback
  // identity changes (which it doesn't, since it's stable from
  // useSessionChanges).
  useEffect(() => {
    if (!onRefresh) return;
    onRefresh(refresh);
  }, [refresh, onRefresh]);

  // Forward loading state so embedded parents can spin their own
  // refresh button while a request is in flight.
  useEffect(() => {
    if (!onLoadingChange) return;
    onLoadingChange(loading);
  }, [loading, onLoadingChange]);

  const Body = (
    <div className="oc-changes-sidebar-body oc-changes-list-body">
      {!enabled && (
        <div className="oc-changes-sidebar-unsupported">
          Changes view is not supported for this platform.
        </div>
      )}
      {enabled && loading && !data && (
        <SidebarFileListSkeleton rows={6} />
      )}
      {enabled && error && (
        <div className="oc-changes-sidebar-error">Failed to load changes: {error}</div>
      )}
      {enabled && data && data.supported && files.length === 0 && !loading && (
        <div className="oc-changes-sidebar-empty">
          No file edits in this session yet.
        </div>
      )}
      {enabled && data && data.supported && files.length > 0 && (
        <>
          <ul className="oc-changes-list">
            {files.slice(0, visibleFileCount).map((change) => (
              <FileChangeGroup
                key={change.path}
                change={change}
              />
            ))}
          </ul>
          {hasMoreFiles && (
            <div
              ref={fileSentinelRef}
              className="oc-diff-sentinel"
              aria-hidden="true"
            />
          )}
        </>
      )}
      {enabled && data && !data.supported && (
        <div className="oc-changes-sidebar-unsupported">
          Changes view is not supported for this platform.
        </div>
      )}
    </div>
  );

  if (embedded) {
    return <div className="oc-changes-sidebar-embedded">{Body}</div>;
  }

  return (
    <aside className="oc-changes-sidebar" aria-label="Session changes">
      <div className="oc-changes-sidebar-header">
        <span className="oc-changes-sidebar-title">
          {filesChanged > 0
            ? `${filesChanged} ${filesChanged === 1 ? 'file' : 'files'} changed`
            : 'Changes'}
        </span>
        <span className="oc-changes-sidebar-counts">
          {filesChanged > 0 && (
            <>
              <span className="oc-changes-add">+{totalAdditions}</span>
              <span className="oc-changes-del">-{totalDeletions}</span>
            </>
          )}
        </span>
        <ChangesRefreshButton onClick={refresh} loading={loading} disabled={!enabled} />
      </div>
      {Body}
    </aside>
  );
}

// ChangesRefreshButton is a small icon button rendered in the
// sidebar/pane header. Disabled when the sidebar is in its
// "not supported" state and visually muted while a request is
// already in flight (so back-to-back clicks don't spam the
// backend; the underlying hook will still abort the previous
// request if one is mid-flight).
interface ChangesRefreshButtonProps {
  onClick: () => void;
  loading?: boolean;
  disabled?: boolean;
}

export function ChangesRefreshButton({ onClick, loading = false, disabled = false }: ChangesRefreshButtonProps) {
  return (
    <button
      type="button"
      className={`oc-changes-refresh-btn${loading ? ' loading' : ''}`}
      onClick={onClick}
      disabled={disabled || loading}
      title="Refresh"
      aria-label="Refresh"
    >
      <i className="bi bi-arrow-clockwise" aria-hidden="true" />
    </button>
  );
}
