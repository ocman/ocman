import { useState } from 'react';
import type { FileChange, SessionEdit } from '../lib/api';
import { DiffView } from './DiffView';
import { RawDiffView } from './RawDiffView';

// The diff body for one session-changes file, shown in the fullscreen
// diff browser. Modern OpenCode parts ship a unified-diff `patch`
// string per edit (RawDiffView); legacy parts only provide
// before/after snapshots, rendered with DiffView (client-side
// simpleDiff). When a file has multiple edits, a disclosure under the
// diff fans them out into per-edit diffs.

interface FileChangeGroupProps {
  change: FileChange;
}

// Renders one diff body for a FileChange or SessionEdit. Prefers
// `patch` (modern schema) over `before`/`after` (legacy schema).
// Centralised so the per-file and per-edit disclosures share the
// same fallback logic.
export function ChangeDiffBody({
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

export function FileChangeGroup({ change }: FileChangeGroupProps) {
  const [showEdits, setShowEdits] = useState(false);

  return (
    <>
      <ChangeDiffBody
        patch={change.patch}
        before={change.before}
        after={change.after}
        filePath={change.path}
      />
      {change.editCount > 1 && (
        <>
          <button
            type="button"
            className="oc-changes-list-edits-toggle"
            onClick={() => setShowEdits((s) => !s)}
          >
            {showEdits ? 'Hide' : 'Show'} {change.editCount} individual edits
          </button>
          {showEdits && (
            <div className="oc-changes-list-body-expanded">
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
  );
}
