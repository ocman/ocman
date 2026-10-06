import { useCallback, useEffect, useMemo, useState } from 'react';
import './UpstreamPane.css';
import type { StateFilter, Upstream } from '../../lib/upstreamApi';
import type { PaneSummary } from '../SessionChangesSidebar';
import { useGitInfo } from '../../lib/useGitInfo';
import { ProjectLabel } from '../ProjectLabel';
import { UpstreamRemoteGroup } from './UpstreamRemoteGroup';
import { clearPRChecksCache } from '../../lib/prChecksCache';
import { useUpstreamPreferences } from '../../lib/upstreamPreferences';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '../Tabs';
import { onGitCommand } from '../../lib/useGlobalEvents';

interface UpstreamPaneProps {
  /** Project directory the upstreams were detected for; keys the lists. */
  directory: string | undefined;
  projectId?: string;
  /**
   * The session's own checkout (e.g. a sibling worktree). Drives the
   * current-branch highlight and where row actions launch. Defaults to
   * `directory`. Changing it alone never reloads the lists.
   */
  currentDirectory?: string;
  /**
   * False while the active session is unresolved: the retained list stays
   * visible but launches are disabled so they can't target the previous
   * checkout or owner.
   */
  actionsEnabled?: boolean;
  remoteId: string;
  upstreams: Upstream[];
  /** Hide controls only after detection confirms there are no supported upstreams. */
  upstreamsReady?: boolean;
  embedded?: boolean;
  // RightPanel API parity. PaneSummary isn't meaningful for the
  // upstream view (no +/- diff counts) so we emit zeros once on
  // mount; the parent renders no numbers next to the title.
  onSummaryChange?: (summary: PaneSummary) => void;
  onRefresh?: (refresh: () => void) => void;
  onLoadingChange?: (loading: boolean) => void;
}

type Tab = 'prs' | 'issues';

/**
 * UpstreamPane renders the PR & Issue browser inside the RightPanel.
 *
 * Layout:
 *
 *   [PRs | Issues]   ← tab strip (with refresh)
 *   ─────────────
 *   <filters: open|closed|all + mine toggle>
 *   <per-remote group(s)>
 *     <header: github.com / forgejo host (only shown when >1 remote)>
 *     <list of rows>
 *     <pagination (prev / next)>
 *
 * State scoped per-tab so flipping between PRs and Issues doesn't
 * reset filters or pagination of the other tab.
 */
export function UpstreamPane({
  directory,
  projectId,
  currentDirectory = directory,
  actionsEnabled = true,
  remoteId,
  upstreams,
  upstreamsReady = true,
  onRefresh,
  onLoadingChange,
  onSummaryChange,
}: UpstreamPaneProps) {
  const { infos: gitInfos } = useGitInfo(currentDirectory && upstreams.length > 0 ? [currentDirectory] : [], remoteId);
  const currentBranch = currentDirectory ? gitInfos[currentDirectory]?.branch : undefined;
  const launchDirectory = actionsEnabled ? currentDirectory : undefined;

  const { tab, prState, issueState, prMine, issueMine } = useUpstreamPreferences((state) => state.preferences);
  const setPreferences = useUpstreamPreferences((state) => state.setPreferences);

  // Tell the parent we have no diff summary to contribute. Done once
  // because the value never changes.
  useEffect(() => {
    onSummaryChange?.({ files: 0, additions: 0, deletions: 0 });
  }, [onSummaryChange]);

  // No remote → explain instead of rendering tabs / filters / lists.
  // The pane stays available in the strip so users can discover the
  // feature even on unsupported projects.
  if (upstreamsReady && upstreams.length === 0) {
    return (
      <div className="oc-upstream-pane" data-testid="upstream-pane">
        <NoUpstreamMessage directory={directory} />
      </div>
    );
  }

  return (
    <Tabs value={tab} onValueChange={(value) => setPreferences({ tab: value === 'issues' ? 'issues' : 'prs' })} className="oc-upstream-pane" data-testid="upstream-pane">
      <TabsList aria-label="Pull requests and issues">
        <TabsTrigger value="prs" data-testid="upstream-tab-prs">PRs</TabsTrigger>
        <TabsTrigger value="issues" data-testid="upstream-tab-issues">Issues</TabsTrigger>
      </TabsList>

      <TabsContent value="prs" className="oc-upstream-panel">
        <UpstreamTabContent
          key="prs"
          kind="prs"
          directory={directory}
          projectId={projectId}
          launchDirectory={launchDirectory}
          remoteId={remoteId}
          upstreams={upstreams}
          state={prState}
          onStateChange={(prState) => setPreferences({ prState })}
          mine={prMine}
          onMineChange={(prMine) => setPreferences({ prMine })}
          onRefresh={onRefresh}
          onLoadingChange={onLoadingChange}
          currentBranch={currentBranch}
        />
      </TabsContent>
      <TabsContent value="issues" className="oc-upstream-panel">
        <UpstreamTabContent
          key="issues"
          kind="issues"
          directory={directory}
          projectId={projectId}
          launchDirectory={launchDirectory}
          remoteId={remoteId}
          upstreams={upstreams}
          state={issueState}
          onStateChange={(issueState) => setPreferences({ issueState })}
          mine={issueMine}
          onMineChange={(issueMine) => setPreferences({ issueMine })}
          onRefresh={onRefresh}
          onLoadingChange={onLoadingChange}
          currentBranch={currentBranch}
        />
      </TabsContent>
    </Tabs>
  );
}

interface UpstreamTabContentProps {
  kind: Tab;
  directory: string | undefined;
  projectId?: string;
  launchDirectory: string | undefined;
  remoteId: string;
  upstreams: Upstream[];
  state: StateFilter;
  onStateChange: (s: StateFilter) => void;
  mine: boolean;
  onMineChange: (m: boolean) => void;
  onRefresh?: (refresh: () => void) => void;
  onLoadingChange?: (loading: boolean) => void;
  currentBranch?: string;
}

function UpstreamTabContent({
  kind,
  directory,
  projectId,
  launchDirectory,
  remoteId,
  upstreams,
  state,
  onStateChange,
  mine,
  onMineChange,
  onRefresh,
  onLoadingChange,
  currentBranch,
}: UpstreamTabContentProps) {
  // Always render the filter strip + per-remote groups. Each group
  // owns its own fetch hook (via UpstreamRemoteGroup below) so a
  // failure in one host doesn't block the other.
  const groupRefreshers = useMemo<Array<() => void>>(() => [], []);

  // Compose a single refresh callback that fans out to every group.
  const refreshAll = useCallback(() => {
    // A manual refresh re-checks CI too, even for SHAs with a cached final status.
    clearPRChecksCache();
    for (const r of groupRefreshers) r();
  }, [groupRefreshers]);

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const unsubscribe = onGitCommand((hint) => {
      if (hint.remoteId !== remoteId) return;
      const sameProject = projectId && projectId !== 'global' && hint.projectId === projectId;
      if (!sameProject && hint.directory !== directory && hint.directory !== launchDirectory) return;
      clearTimeout(timer);
      timer = setTimeout(refreshAll, 750);
    });
    return () => { unsubscribe(); clearTimeout(timer); };
  }, [directory, launchDirectory, projectId, remoteId, refreshAll]);

  useEffect(() => {
    onRefresh?.(refreshAll);
  }, [refreshAll, onRefresh]);

  // Loading is "any group still loading" — UpstreamRemoteGroup
  // pushes its loading state up through onLoadingChange below.
  const [loadingCount, setLoadingCount] = useState(0);
  useEffect(() => {
    onLoadingChange?.(loadingCount > 0);
  }, [loadingCount, onLoadingChange]);
  const handleGroupLoading = useCallback((loading: boolean) => {
    setLoadingCount((n) => (loading ? n + 1 : Math.max(0, n - 1)));
  }, []);

  const showGroupHeader = upstreams.length > 1;

  // UpstreamPane guarantees upstreams.length > 0 by the time we get
  // here (the no-upstream case is handled one level up so the tab
  // strip is hidden along with the lists).
  return (
    <div className="oc-upstream-tab-content">
      <FilterStrip
        state={state}
        onStateChange={onStateChange}
        mine={mine}
        onMineChange={onMineChange}
      />
      {upstreams.map((u) => (
        <UpstreamRemoteGroup
          key={`${remoteId}/${directory}/${u.host}/${u.remote}`}
          kind={kind}
          upstream={u}
          directory={directory!}
          launchDirectory={launchDirectory}
          remoteId={remoteId}
          state={state}
          mine={mine}
          registerRefresh={(fn) => {
            groupRefreshers.push(fn);
            return () => {
              const i = groupRefreshers.indexOf(fn);
              if (i >= 0) groupRefreshers.splice(i, 1);
            };
          }}
          onLoadingChange={handleGroupLoading}
          showHeader={showGroupHeader}
          currentBranch={currentBranch}
        />
      ))}
    </div>
  );
}

interface FilterStripProps {
  state: StateFilter;
  onStateChange: (s: StateFilter) => void;
  mine: boolean;
  onMineChange: (m: boolean) => void;
}

function FilterStrip({ state, onStateChange, mine, onMineChange }: FilterStripProps) {
  return (
    <div className="oc-upstream-filters" role="toolbar">
      <div className="oc-upstream-filter-group" role="radiogroup" aria-label="State">
        {(['open', 'closed', 'all'] as StateFilter[]).map((s) => (
          <button
            key={s}
            role="radio"
            aria-checked={state === s}
            className={`oc-upstream-filter${state === s ? ' active' : ''}`}
            onClick={() => onStateChange(s)}
            data-testid={`upstream-filter-${s}`}
          >
            {s}
          </button>
        ))}
      </div>
      <label className="oc-upstream-mine">
        <input
          type="checkbox"
          checked={mine}
          onChange={(e) => onMineChange(e.target.checked)}
          data-testid="upstream-filter-mine"
        />
        Mine
      </label>
    </div>
  );
}

/**
 * NoUpstreamMessage renders when the project has no GitHub/Forgejo
 * remote. Kept informational rather than apologetic — the user
 * intentionally opened the pane, so spell out what *would* make it
 * work.
 */
function NoUpstreamMessage({ directory }: { directory: string | undefined }) {
  return (
    <div className="oc-upstream-no-upstream" data-testid="upstream-no-upstream">
      <p className="oc-upstream-no-upstream-title">No supported upstream detected</p>
      <p>
        Ocman looks for git remotes pointing at <strong>github.com</strong> or a
        Forgejo host configured in <code>~/.config/tea/config.yml</code>.
      </p>
      {directory ? (
        <p className="oc-upstream-no-upstream-hint">
          Current project: <code><ProjectLabel path={directory} /></code>
        </p>
      ) : null}
      <p className="oc-upstream-no-upstream-hint">
        To enable this pane, add a remote (<code>git remote add origin …</code>)
        or run <code>tea login add</code> for your Forgejo server.
      </p>
    </div>
  );
}
