import { useState } from 'react';
import type { FileChange, SessionEdit } from '../lib/api';
import { DiffView } from './DiffView';
import { RawDiffView } from './RawDiffView';

// One row in the changes sidebar: header with filename + counts,
// expand toggle, and an optional disclosure listing each individual
// edit (rather than the collapsed file view).
//
// Modern OpenCode parts ship a unified-diff `patch` string per edit,
// which we render with RawDiffView. Legacy parts only provide
// before/after snapshots, in which case we fall back to DiffView
// (which calls simpleDiff to compute the diff client-side).
//
// The collapsed diff is always shown in expanded state; the
// "individual edits" disclosure is opt-in because for a file edited
// once it's identical to the collapsed diff.

interface FileChangeGroupProps {
  change: FileChange;
  defaultExpanded?: boolean;
}

// Renders one diff body for a FileChange or SessionEdit. Prefers
// `patch` (modern schema) over `before`/`after` (legacy schema).
// Centralised so the per-file and per-edit disclosures share the
// same fallback logic.
function ChangeDiffBody({
  patch,
  before,
  after,
  filePath,
}: {
  patch?: string;
  before?: string;
  after?: string;
  filePath: string;
}) {
  if (patch && patch.length > 0) {
    return <RawDiffView diff={patch} filePath={filePath} />;
  }
  return (
    <DiffView
      before={before ?? ''}
      after={after ?? ''}
      filePath={filePath}
    />
  );
}

export function FileChangeGroup({ change, defaultExpanded = true }: FileChangeGroupProps) {
  const [expanded, setExpanded] = useState(defaultExpanded);
  const [showEdits, setShowEdits] = useState(false);

  const hasMultipleEdits = change.editCount > 1;

  return (
    <div className="oc-change-group">
      <div
        className="oc-change-group-header"
        onClick={() => setExpanded((e) => !e)}
        role="button"
        tabIndex={0}
        aria-expanded={expanded}
      >
        <span className="oc-change-group-name" title={change.path}>
          {change.displayPath || change.path}
        </span>
        <span className="oc-change-group-counts">
          <span className="oc-changes-add">+{change.additions}</span>
          <span className="oc-changes-del">-{change.deletions}</span>
        </span>
      </div>
      {expanded && (
        <>
          <div className="oc-change-group-body">
            <ChangeDiffBody
              patch={change.patch}
              before={change.before}
              after={change.after}
              filePath={change.path}
            />
          </div>
          {hasMultipleEdits && (
            <>
              <button
                type="button"
                className="oc-change-group-edits-toggle"
                onClick={() => setShowEdits((s) => !s)}
              >
                {showEdits ? 'Hide' : 'Show'} {change.editCount} individual edits
              </button>
              {showEdits && (
                <div>
                  {change.edits.map((edit: SessionEdit, i) => (
                    <div key={edit.partId} className="oc-change-edit">
                      <div className="oc-change-edit-meta">
                        Edit {i + 1} of {change.editCount}
                        {' \u2022 '}
                        <span className="oc-changes-add">+{edit.additions}</span>
                        {' '}
                        <span className="oc-changes-del">-{edit.deletions}</span>
                      </div>
                      <ChangeDiffBody
                        patch={edit.patch}
                        before={edit.before}
                        after={edit.after}
                        filePath={change.path}
                      />
                    </div>
                  ))}
                </div>
              )}
            </>
          )}
        </>
      )}
    </div>
  );
}
