import { useState, type ReactNode } from 'react';

interface ChangedFileRowProps {
  path: string;
  displayPath?: string;
  additions: number;
  deletions: number;
  defaultExpanded?: boolean;
  statusBadge?: ReactNode;
  children: ReactNode;
  expandedFooter?: ReactNode;
}

export function ChangedFileRow({ path, displayPath, additions, deletions, defaultExpanded = false, statusBadge, children, expandedFooter }: ChangedFileRowProps) {
  const [expanded, setExpanded] = useState(defaultExpanded);

  return (
    <li>
      <button
        type="button"
        className="oc-changes-list-row"
        onClick={() => setExpanded((e) => !e)}
        aria-expanded={expanded}
      >
        {statusBadge}
        <span className="oc-changes-list-path" title={path}>
          {displayPath || path}
        </span>
        <span className="oc-changes-list-counts">
          {additions > 0 && <span className="oc-changes-add">+{additions}</span>}
          {deletions > 0 && <span className="oc-changes-del">-{deletions}</span>}
        </span>
      </button>
      {expanded && (
        <>
          <div className="oc-changes-list-body-expanded">{children}</div>
          {expandedFooter}
        </>
      )}
    </li>
  );
}
