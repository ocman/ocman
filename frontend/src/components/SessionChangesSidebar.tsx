import { useMemo } from 'react';
import type { FileChange } from '../lib/api';
import { usePlatformCapabilities } from '../lib/useCapabilities';
import { useSessionChanges } from '../lib/useSessionChanges';
import { FileChangeGroup } from './FileChangeGroup';
import { ChangedFileRow } from './ChangedFileRow';
import type { FullscreenDiffFile } from './DiffFullscreenModal';
import { ChangesListPane, type ChangesPaneCallbacks } from './ChangesListPane';

// PaneSummary is the per-tab counts surfaced to the parent so it can
// render "N files +A -D" next to the pane title. Both sidebars emit
// the same shape so RightPanel can render either uniformly.
export interface PaneSummary {
  files: number;
  additions: number;
  deletions: number;
}

interface SessionChangesSidebarProps extends ChangesPaneCallbacks {
  sessionId: string;
  // Platform ID for the current session — used to look up
  // capability flags. When undefined (loading), the sidebar
  // renders a loading skeleton. AD-12a: never branched on directly.
  platformId: string | undefined;
  // Increments whenever the parent observes a new edit/write part
  // arriving via SSE, prompting a debounced re-fetch. Pass 0 (or
  // omit) for static views (e.g. archived sessions).
  dirtyTick?: number;
}

export function SessionChangesSidebar({ sessionId, platformId, dirtyTick, ...callbacks }: SessionChangesSidebarProps) {
  const caps = usePlatformCapabilities(platformId);
  const enabled = caps.fileChanges;
  const { data, loading, error, refresh } = useSessionChanges(sessionId, { enabled, dirtyTick });

  // Defensive defaults: a faulty backend / older deployment could
  // ship `null` instead of `[]` for files, or omit fields entirely.
  // Coerce here so the rest of the component never has to think
  // about it.
  // Memoised so the empty fallback keeps a stable identity (a fresh
  // `[]` each render would thrash the downstream useMemo).
  const files = useMemo(() => data?.files ?? [], [data]);
  const fullscreenFiles = useMemo(() => toFullscreenFiles(files), [files]);
  const supported = !!data?.supported;

  return (
    <ChangesListPane
      {...callbacks}
      enabled={enabled}
      hasData={!!data}
      loading={loading}
      error={error}
      refresh={refresh}
      files={supported ? files : []}
      summary={{
        files: data?.filesChanged ?? files.length,
        additions: data?.totalAdditions ?? 0,
        deletions: data?.totalDeletions ?? 0,
      }}
      fullscreenTitle="Session changes"
      fullscreenFiles={fullscreenFiles}
      loadErrorLabel="Failed to load changes"
      emptyMessage={supported ? 'No file edits in this session yet.' : null}
      notice={(!enabled || (data && !supported)) && (
        <div className="oc-changes-sidebar-unsupported">
          Changes view is not supported for this platform.
        </div>
      )}
      renderRows={(visible, openFile) => (
        <ul className="oc-changes-list">
          {visible.map((change) => (
            <ChangedFileRow
              key={change.path}
              path={change.path}
              displayPath={change.displayPath}
              additions={change.additions}
              deletions={change.deletions}
              onOpen={() => openFile(change.path)}
            />
          ))}
        </ul>
      )}
    />
  );
}

// toFullscreenFiles adapts the session-changes payload to the
// fullscreen diff browser's file shape.
function toFullscreenFiles(files: FileChange[]): FullscreenDiffFile[] {
  return files.map((change) => ({
    key: change.path,
    path: change.path,
    label: change.displayPath || change.path,
    additions: change.additions,
    deletions: change.deletions,
    body: <FileChangeGroup change={change} />,
  }));
}
