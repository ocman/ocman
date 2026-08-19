import { useState } from 'react';
import type { FileChange, SessionEdit } from '../lib/api';
import { DiffView } from './DiffView';
import { RawDiffView } from './RawDiffView';

// One row in the session-changes sidebar: a single line with the
// filename + total +A/-D, click to reveal the diff body inline.
// Visually mirrors WorkingTreeChangesSidebar's row style so both
// panes feel consistent.
//
// Rows are collapsed by default (`defaultExpanded={false}`) — opening
// a session shouldn't dump every diff at once. Multiple rows can be
// open at the same time; toggle state is local to each row.
//
// Modern OpenCode parts ship a unified-diff `patch` string per edit,
// which we render with RawDiffView. Legacy parts only provide
// before/after snapshots, in which case we fall back to DiffView
// (which calls simpleDiff to compute the diff client-side).
//
// When a file has multiple edits, an additional disclosure under the
// diff body lets the user fan them out into per-edit diffs.

interface FileChangeGroupProps {
  change: FileChange;
  defaultExpanded?: boolean;
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

export function FileChangeGroup({ change, defaultExpanded = false }: FileChangeGroupProps) {
  const [expanded, setExpanded] = useState(defaultExpanded);
  const [showEdits, setShowEdits] = useState(false);

  const hasMultipleEdits = change.editCount > 1;

  return (
    <li>
      <button
        type="button"
        className="oc-changes-list-row"
        onClick={() => setExpanded((e) => !e)}
        aria-expanded={expanded}
      >
        <span className="oc-changes-list-path" title={change.path}>
          {change.displayPath || change.path}
        </span>
        <span className="oc-changes-list-counts">
          {change.additions > 0 && <span className="oc-changes-add">+{change.additions}</span>}
          {change.deletions > 0 && <span className="oc-changes-del">-{change.deletions}</span>}
        </span>
      </button>
      {expanded && (
        <>
          <div className="oc-changes-list-body-expanded">
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
      )}
    </li>
  );
}
