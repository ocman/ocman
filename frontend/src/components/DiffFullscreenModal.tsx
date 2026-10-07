import { useState } from 'react';
import type { ReactNode } from 'react';
import { FileBrowserModal } from './FileBrowserModal';
import { IconButton } from './IconButton';
import { ChangedFilesTree } from './ChangedFilesTree';
import { EmptyState } from './EmptyState';

// One entry in the fullscreen diff browser. `body` is the already-
// built diff element for the file; React only renders the selected
// one, so handing over the whole list costs nothing.
export interface FullscreenDiffFile {
  key: string;
  path: string;
  // Structured source path for renames. Keeping this separate avoids
  // parsing the human-readable arrow label back into paths.
  oldPath?: string;
  // Optional display label (e.g. "old → new" for renames). Defaults
  // to `path`.
  label?: string;
  // Optional `git status -s` short code rendered as a badge. The
  // class suffix is the caller's status string.
  status?: string;
  statusLabel?: string;
  additions: number;
  deletions: number;
  body: ReactNode;
}

interface DiffFullscreenModalProps {
  title: string;
  files: FullscreenDiffFile[];
  // Key of the file to open on; falls back to the first file.
  initialKey?: string;
  onClose: () => void;
}

// DiffFullscreenModal shows the same per-file diffs as the sidebar
// panes, with a file tree beside the selected diff (above it on phones).
export function DiffFullscreenModal({ title, files, initialKey, onClose }: DiffFullscreenModalProps) {
  const [selectedKey, setSelectedKey] = useState<string | null>(initialKey || (files[0]?.key ?? null));
  // Selecting by key (not index) keeps the selection stable across a
  // background refresh; fall back to the first file when the selected
  // one disappears.
  const current = files.find((f) => f.key === selectedKey) ?? files[0];

  return (
    <FileBrowserModal
      onClose={onClose}
      title={title}
      description={`${files.length} ${files.length === 1 ? 'file' : 'files'}`}
      dialogTestId="diff-fullscreen"
      sidebar={<ChangedFilesTree files={files} selectedKey={current?.key ?? null} onSelect={setSelectedKey} />}
    >
      {current ? current.body : <EmptyState>No changes to show.</EmptyState>}
    </FileBrowserModal>
  );
}

export function FullscreenButton({ onClick, disabled = false }: { onClick: () => void; disabled?: boolean }) {
  return <IconButton icon="bi-arrows-fullscreen" label="Fullscreen" size="compact" variant="ghost" onClick={onClick} disabled={disabled} />;
}
