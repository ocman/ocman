import type { WorkingTreeFile } from '../lib/api';

// A visual section in the working-tree sidebar. Each section is a
// header row ("Changed (3)", "Untracked (5)") followed by the files
// it contains. Empty sections are not emitted.
export interface WorkingTreeFileGroup {
  // Stable identifier — used as a React key and for collapse-state
  // bookkeeping. Not user-visible.
  id: 'changed' | 'untracked';
  // Human-readable label rendered next to the count.
  label: string;
  files: WorkingTreeFile[];
}

// groupWorkingTreeFiles partitions the flat /api/git/diff response
// into the sections rendered by WorkingTreeChangesSidebar. Today we
// surface two: tracked Changes (modified/added/deleted/renamed) and
// Untracked. Section order is fixed (Changed first, then Untracked)
// to match the screenshot reference and keep render output stable.
//
// Within a section the original input order is preserved so the user
// sees the same ordering they'd get from `git status`.
export function groupWorkingTreeFiles(files: WorkingTreeFile[]): WorkingTreeFileGroup[] {
  const changed: WorkingTreeFile[] = [];
  const untracked: WorkingTreeFile[] = [];
  for (const f of files) {
    if (f.status === 'untracked') {
      untracked.push(f);
    } else {
      changed.push(f);
    }
  }
  const groups: WorkingTreeFileGroup[] = [];
  if (changed.length > 0) {
    groups.push({ id: 'changed', label: 'Changed', files: changed });
  }
  if (untracked.length > 0) {
    groups.push({ id: 'untracked', label: 'Untracked', files: untracked });
  }
  return groups;
}
