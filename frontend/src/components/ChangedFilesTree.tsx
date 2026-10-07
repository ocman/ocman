import { useEffect, useLayoutEffect, useMemo, useRef } from 'react';
import type { GitStatus, GitStatusEntry } from '@pierre/trees';
import { FileTree, useFileTree } from '@pierre/trees/react';
import type { FullscreenDiffFile } from './DiffFullscreenModal';
import styles from './ChangedFilesTree.module.css';
import { FILE_TREE_SELECTION_CSS } from './FileBrowserModal';

const GIT_STATUSES = new Set<string>(['added', 'deleted', 'ignored', 'modified', 'renamed', 'untracked']);

// Renames sit at their new path (the badge says "renamed"); otherwise the
// display label wins so session changes show their repo-relative path.
function treePath(f: FullscreenDiffFile): string {
  return f.oldPath ? f.path : (f.label ?? f.path);
}

interface ChangedFilesTreeProps {
  files: FullscreenDiffFile[];
  selectedKey: string | null;
  onSelect: (key: string) => void;
}

// ChangedFilesTree renders the changed files as a collapsible directory
// tree (@pierre/trees). The model is created once, so every callback it
// holds reads the latest props through `live`.
export function ChangedFilesTree({ files, selectedKey, onSelect }: ChangedFilesTreeProps) {
  const byPath = useMemo(() => new Map(files.map((f) => [treePath(f), f])), [files]);
  const paths = useMemo(() => [...byPath.keys()], [byPath]);
  const gitStatus = useMemo<GitStatusEntry[]>(
    () => files.flatMap((f) => (f.status && GIT_STATUSES.has(f.status)
      ? [{ path: treePath(f), status: f.status as GitStatus }]
      : [])),
    [files],
  );
  const selectedPath = files.find((f) => f.key === selectedKey);

  const live = useRef({ byPath, onSelect });
  useLayoutEffect(() => { live.current = { byPath, onSelect }; });

  const { model } = useFileTree({
    paths,
    gitStatus,
    flattenEmptyDirectories: true,
    initialExpansion: 'open',
    // Keep the +/- counts whole; the file name truncates instead.
    unsafeCSS: `${FILE_TREE_SELECTION_CSS}\n[data-item-section="decoration"] { flex: none; margin-left: auto; }`,
    initialSelectedPaths: selectedPath ? [treePath(selectedPath)] : [],
    onSelectionChange: (selected) => {
      // Directories are selectable too; only a file changes the diff.
      const file = [...selected].reverse().map((p) => live.current.byPath.get(p)).find(Boolean);
      if (file) live.current.onSelect(file.key);
    },
    renderRowDecoration: ({ item }) => {
      const f = live.current.byPath.get(item.path);
      if (!f || item.kind !== 'file') return null;
      const parts = [
        f.additions > 0 && { text: `+${f.additions} `, color: 'var(--accent2)' },
        f.deletions > 0 && { text: `-${f.deletions}`, color: 'var(--danger)' },
      ].filter((p) => p !== false);
      return parts.length ? { text: '', parts, title: f.label ?? f.path } : null;
    },
  });

  // Opening on a file from the sidebar: bring it into view.
  const initialPath = useRef(selectedPath && treePath(selectedPath));
  useEffect(() => {
    if (initialPath.current) model.scrollToPath(initialPath.current, { focus: false });
  }, [model]);

  // A background refresh replaces the file list; the model is long-lived.
  const first = useRef(true);
  useEffect(() => {
    if (first.current) { first.current = false; return; }
    model.resetPaths(paths);
    model.setGitStatus(gitStatus);
  }, [model, paths, gitStatus]);

  return (
    <FileTree
      model={model}
      className={styles.tree}
      aria-label="Changed files"
      data-testid="changed-files-tree"
    />
  );
}
