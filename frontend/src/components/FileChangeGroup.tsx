import { useState } from 'react';
import type { FileChange } from '../lib/api';
import { DiffView } from './DiffView';

// One row in the changes sidebar: header with filename + counts,
// expand toggle, and an optional disclosure listing each individual
// edit (rather than the first-before / last-after collapsed view).
//
// The collapsed diff is always shown in expanded state; the
// "individual edits" disclosure is opt-in because for a file edited
// once it's identical to the collapsed diff.

interface FileChangeGroupProps {
  change: FileChange;
  defaultExpanded?: boolean;
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
            <DiffView
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
                  {change.edits.map((edit, i) => (
                    <div key={edit.partId} className="oc-change-edit">
                      <div className="oc-change-edit-meta">
                        Edit {i + 1} of {change.editCount}
                        {' \u2022 '}
                        <span className="oc-changes-add">+{edit.additions}</span>
                        {' '}
                        <span className="oc-changes-del">-{edit.deletions}</span>
                      </div>
                      <DiffView
                        // Each individual edit only carries the
                        // before/after for that single change. Fall
                        // back to empty strings so DiffView shows
                        // "(no changes)" rather than crashing.
                        before={edit.before ?? ''}
                        after={edit.after ?? ''}
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
