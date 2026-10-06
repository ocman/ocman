import { useCallback, useEffect, useRef, useState } from 'react';
import type { ChangesSidebarTab } from '../lib/uiStore';
import { SessionChangesSidebar, type PaneSummary } from './SessionChangesSidebar';
import { WorkingTreeChangesSidebar } from './WorkingTreeChangesSidebar';
import { SessionInfoSidebar } from './SessionInfoSidebar';
import { UpstreamPane } from './upstream/UpstreamPane';
import { ErrorBoundary } from './ErrorBoundary';
import type { Session, SessionInfoCommit } from '../lib/api';
import type { MessageBookmark, MessageBookmarkGroup } from '../lib/messageBookmarks';
import { MessageBookmarksPane } from './MessageBookmarksPane';
import { BeadsPane } from './BeadsPane';
import { ArtifactsPane } from './ArtifactsPane';
import type { useBeadsStatus } from '../lib/useBeadsStatus';
import type { ProjectTarget } from '../lib/useProjectTarget';
import { PaneHeader } from './RightPanelPaneHeader';

function UpstreamDetectionStatus() {
  const [visible, setVisible] = useState(false);
  useEffect(() => {
    // ponytail: delay only the status; upstream detection still starts immediately.
    const timeout = setTimeout(() => setVisible(true), 200);
    return () => clearTimeout(timeout);
  }, []);
  return visible ? <div role="status">Detecting upstreams…</div> : null;
}

export interface PaneProps {
  tab: ChangesSidebarTab;
  sessionId: string;
  platformId: string | undefined;
  directory: string | undefined;
  dirtyTick?: number;
  // Forwarded to SessionInfoSidebar's Session section. Other panes
  // ignore it. Tokens and Todos for the same pane come from the
  // /api/session/{id}/info endpoint, not from props.
  session?: Session;
  messageBookmarkGroups: MessageBookmarkGroup[];
  selectedMessageBookmarkKey: string | null;
  onRemoveMessageBookmark: (bookmark: MessageBookmark) => void;
  onScrollToMessageBookmark: (bookmark: MessageBookmark) => void;
  onNavigateCommit?: (commit: SessionInfoCommit) => void;
  commitSourceStatus?: string | null;
  // Upstream remotes for the current project. Only the 'upstream' pane
  // consumes this; the other panes ignore it. Resolved at RightPanel
  // level so we don't re-detect per pane. upstreamTarget is the project
  // directory/owner they were detected for (pinned across sibling worktrees).
  upstreamTarget: ProjectTarget;
  upstreams: import('../lib/upstreamApi').Upstream[];
  upstreamLoading: boolean;
  upstreamError: string | null;
  refreshUpstreams: () => void;
  beadsResult: ReturnType<typeof useBeadsStatus>;
  divider: boolean;
  size: number;
  // When non-null, the pane header doubles as a resize handle for
  // the boundary between pane[resizeAboveIdx] and this pane.
  resizeAboveIdx: number | null;
  openTabs: ChangesSidebarTab[];
  sizes: number[];
  onResize: (idx: number, beforeSize: number, afterSize: number) => void;
}

export function Pane({
  tab,
  sessionId,
  platformId,
  directory,
  dirtyTick,
  session,
  messageBookmarkGroups,
  selectedMessageBookmarkKey,
  onRemoveMessageBookmark,
  onScrollToMessageBookmark,
  onNavigateCommit,
  commitSourceStatus,
  upstreamTarget,
  upstreams,
  upstreamLoading,
  upstreamError,
  refreshUpstreams,
  beadsResult,
  divider,
  size,
  resizeAboveIdx,
  openTabs,
  sizes,
  onResize,
}: PaneProps) {
  // Children push their summary up via onSummaryChange so we can
  // render it next to the title without coupling RightPanel to
  // each view's data hook.
  const [summary, setSummary] = useState<PaneSummary>({ files: 0, additions: 0, deletions: 0 });
  // useCallback keeps the function reference stable across renders
  // so the child's effect doesn't loop (it depends on
  // onSummaryChange identity).
  const handleSummary = useCallback((s: PaneSummary) => setSummary(s), []);

  // Refresh callback exposed by the embedded sidebar. Held in a ref
  // so re-renders don't reset it; surfaced through state only so the
  // refresh button knows when the callback is actually wired up.
  const refreshRef = useRef<(() => void) | null>(null);
  const [hasRefresh, setHasRefresh] = useState(false);
  const handleRefresh = useCallback((fn: () => void) => {
    refreshRef.current = fn;
    setHasRefresh(true);
  }, []);
  const onRefreshClick = useCallback(() => {
    refreshRef.current?.();
  }, []);
  // Mirror the embedded sidebar's loading flag so the refresh button
  // in the pane header can spin its icon while a request is in flight.
  const [loading, setLoading] = useState(false);
  const handleLoadingChange = useCallback((next: boolean) => setLoading(next), []);

  // Same ref+flag dance as refresh, for the pane's fullscreen diff
  // browser. Only the diff panes wire it up; the rest never set the
  // flag so no button appears.
  const fullscreenRef = useRef<(() => void) | null>(null);
  const [hasFullscreen, setHasFullscreen] = useState(false);
  const handleFullscreen = useCallback((fn: () => void) => {
    fullscreenRef.current = fn;
    setHasFullscreen(true);
  }, []);
  const onFullscreenClick = useCallback(() => {
    fullscreenRef.current?.();
  }, []);

  return (
    <>
      <PaneHeader
        tab={tab}
        divider={divider}
        summary={summary}
        hasRefresh={hasRefresh}
        loading={loading}
        onRefreshClick={onRefreshClick}
        hasFullscreen={hasFullscreen}
        onFullscreenClick={onFullscreenClick}
        resizeAboveIdx={resizeAboveIdx}
        openTabs={openTabs}
        sizes={sizes}
        onResize={onResize}
      />
      <div className="oc-right-panel-pane" style={{ flexGrow: size, flexBasis: 0 }}>
        {/* Each pane gets its own boundary so a crash in one (bad diff
            payload, broken markdown, etc.) doesn't take the other panes
            down. resetKey on sessionId clears stale crashes when the user
            switches sessions. */}
        <ErrorBoundary name={`right-panel:${tab}`} inline resetKey={sessionId}>
          {tab === 'info' && (
            <SessionInfoSidebar
              sessionId={sessionId}
              platformId={platformId}
              dirtyTick={dirtyTick}
              session={session}
              embedded
              onSummaryChange={handleSummary}
              onRefresh={handleRefresh}
              onLoadingChange={handleLoadingChange}
              onNavigateCommit={onNavigateCommit}
              commitSourceStatus={commitSourceStatus}
            />
          )}
          {tab === 'session' && (
            <SessionChangesSidebar
              sessionId={sessionId}
              platformId={platformId}
              dirtyTick={dirtyTick}
              embedded
              onSummaryChange={handleSummary}
              onRefresh={handleRefresh}
              onLoadingChange={handleLoadingChange}
              onFullscreen={handleFullscreen}
            />
          )}
          {tab === 'working-tree' && (
            <WorkingTreeChangesSidebar
              directory={directory}
              dirtyTick={dirtyTick}
              embedded
              onSummaryChange={handleSummary}
              onRefresh={handleRefresh}
              onLoadingChange={handleLoadingChange}
              onFullscreen={handleFullscreen}
            />
          )}
          {tab === 'bookmarks' && (
            <MessageBookmarksPane
              groups={messageBookmarkGroups}
              selectedKey={selectedMessageBookmarkKey}
              onRemove={onRemoveMessageBookmark}
              onScrollToMessage={onScrollToMessageBookmark}
            />
          )}
          {tab === 'upstream' && upstreamLoading && <UpstreamDetectionStatus key={`${upstreamTarget.remoteId}\0${upstreamTarget.directory}`} />}
          {tab === 'upstream' && upstreamError && (
            <div role="alert">
              {upstreamError} <button type="button" onClick={refreshUpstreams}>Retry</button>
            </div>
          )}
          {tab === 'upstream' && (
            <UpstreamPane
              directory={upstreamTarget.directory}
              projectId={upstreamTarget.projectId}
              currentDirectory={directory}
              actionsEnabled={session?.id === sessionId && !!directory}
              remoteId={upstreamTarget.remoteId}
              upstreams={upstreams}
              upstreamsReady={!upstreamLoading && !upstreamError}
              onSummaryChange={handleSummary}
              onRefresh={handleRefresh}
              onLoadingChange={handleLoadingChange}
            />
          )}
          {tab === 'beads' && beadsResult.data?.available && (
            <BeadsPane
              status={beadsResult.data}
              loading={beadsResult.isFetching}
              error={beadsResult.error}
              refresh={beadsResult.refetch}
              onRefresh={handleRefresh}
              onLoadingChange={handleLoadingChange}
            />
          )}
          {tab === 'artifacts' && (
            <ArtifactsPane sessionId={sessionId} platformId={platformId} directory={directory} />
          )}
        </ErrorBoundary>
      </div>
    </>
  );
}
