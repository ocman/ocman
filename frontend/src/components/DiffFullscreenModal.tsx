import { useState } from 'react';
import type { ReactNode } from 'react';
import './DiffFullscreenModal.css';
import { Modal } from './Modal';

// One entry in the fullscreen diff browser. `body` is the already-
// built diff element for the file; React only renders the selected
// one, so handing over the whole list costs nothing.
export interface FullscreenDiffFile {
  key: string;
  path: string;
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
  onClose: () => void;
}

// DiffFullscreenModal shows the same per-file diffs as the sidebar
// panes, but in a 95vw/95vh two-column layout: file names on the
// left, the selected file's diff on the right.
export function DiffFullscreenModal({ title, files, onClose }: DiffFullscreenModalProps) {
  const [selectedKey, setSelectedKey] = useState<string | null>(files[0]?.key ?? null);
  // Selecting by key (not index) keeps the selection stable across a
  // background refresh; fall back to the first file when the selected
  // one disappears.
  const current = files.find((f) => f.key === selectedKey) ?? files[0];

  return (
    <Modal
      onClose={onClose}
      label={title}
      backdropClassName="oc-diff-fs-backdrop"
      dialogClassName="oc-diff-fs-modal"
      dialogTestId="diff-fullscreen"
    >
      <header className="oc-diff-fs-header">
        <h2>{title}</h2>
        <span className="oc-diff-fs-header-count">
          {files.length} {files.length === 1 ? 'file' : 'files'}
        </span>
        <button
          type="button"
          className="oc-diff-fs-close"
          onClick={onClose}
          aria-label="Close"
          title="Close"
        >
          <i className="bi bi-x-lg" aria-hidden="true" />
        </button>
      </header>
      <div className="oc-diff-fs-cols">
        <ul className="oc-diff-fs-files" aria-label="Changed files">
          {files.map((f) => {
            const displayPath = splitDisplayPath(f.label ?? f.path);
            return <li key={f.key}>
              <button
                type="button"
                className={`oc-diff-fs-file${f.key === current?.key ? ' selected' : ''}`}
                onClick={() => setSelectedKey(f.key)}
                aria-current={f.key === current?.key}
                title={f.label ?? f.path}
              >
                {f.status && (
                  <span
                    className={`oc-change-group-status oc-change-group-status-${f.status}`}
                    title={f.status}
                  >
                    {f.statusLabel ?? f.status.charAt(0).toUpperCase()}
                  </span>
                )}
                <span className="oc-diff-fs-file-name">{displayPath.name}</span>
                <span className="oc-diff-fs-file-dir">{displayPath.dir}</span>
                <span className="oc-diff-fs-file-counts">
                  {f.additions > 0 && <span className="oc-changes-add">+{f.additions}</span>}
                  {f.deletions > 0 && <span className="oc-changes-del">-{f.deletions}</span>}
                </span>
              </button>
            </li>;
          })}
        </ul>
        <div className="oc-diff-fs-diff">
          {current ? (
            <>
              <div className="oc-diff-fs-diff-path">{current.label ?? current.path}</div>
              {current.body}
            </>
          ) : (
            <div className="oc-diff-empty">No changes to show.</div>
          )}
        </div>
      </div>
    </Modal>
  );
}

function basename(path: string): string {
  const i = path.lastIndexOf('/');
  return i === -1 ? path : path.slice(i + 1);
}

function dirname(path: string): string {
  const i = path.lastIndexOf('/');
  return i === -1 ? '' : path.slice(0, i);
}

function splitDisplayPath(label: string): { name: string; dir: string } {
  const paths = label.split(' → ');
  return {
    name: paths.map(basename).join(' → '),
    dir: [...new Set(paths.map(dirname))].join(' → '),
  };
}

// FullscreenButton is the header icon button that opens the modal.
// Shares the refresh button's styling so the header reads as one
// group of actions.
export function FullscreenButton({ onClick, disabled = false }: { onClick: () => void; disabled?: boolean }) {
  return (
    <button
      type="button"
      className="oc-changes-refresh-btn"
      onClick={onClick}
      disabled={disabled}
      title="Fullscreen"
      aria-label="Fullscreen"
    >
      <i className="bi bi-arrows-fullscreen" aria-hidden="true" />
    </button>
  );
}
