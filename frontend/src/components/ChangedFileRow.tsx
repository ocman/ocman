import type { ReactNode } from 'react';

interface ChangedFileRowProps {
  path: string;
  displayPath?: string;
  additions: number;
  deletions: number;
  statusBadge?: ReactNode;
  // Opens the fullscreen diff browser on this file.
  onOpen: () => void;
}

export function ChangedFileRow({ path, displayPath, additions, deletions, statusBadge, onOpen }: ChangedFileRowProps) {
  return (
    <li>
      <button type="button" className="oc-changes-list-row" onClick={onOpen}>
        {statusBadge}
        <span className="oc-changes-list-path" title={path}>
          {displayPath || path}
        </span>
        <span className="oc-changes-list-counts">
          {additions > 0 && <span className="oc-changes-add">+{additions}</span>}
          {deletions > 0 && <span className="oc-changes-del">-{deletions}</span>}
        </span>
      </button>
    </li>
  );
}
