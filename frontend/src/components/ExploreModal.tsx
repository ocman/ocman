import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { File } from '@pierre/diffs/react';
import { FileTree, useFileTree } from '@pierre/trees/react';
import { api } from '../lib/api';
import type { RepoFileContent, RepoFileList } from '../lib/api.types';
import { shortPath } from '../lib/format';
import { Modal } from './Modal';
import { IconButton } from './IconButton';
import { EmptyState } from './EmptyState';
import { LoadingState } from './LoadingState';
import { FILE_OPTIONS } from './diffOptions';
import './DiffFullscreenModal.css';

type Load<T> = { data?: T; error?: string };

// useLoad fetches once per key change, dropping stale responses.
function useLoad<T>(key: string | null, load: (signal: AbortSignal) => Promise<T>): Load<T> & { loading: boolean } {
  const [state, setState] = useState<Load<T> & { key?: string }>({});
  const loadRef = useRef(load);
  useLayoutEffect(() => { loadRef.current = load; });
  useEffect(() => {
    if (key === null) return;
    const ctrl = new AbortController();
    loadRef.current(ctrl.signal).then(
      (data) => setState({ key, data }),
      (err: unknown) => { if (!ctrl.signal.aborted) setState({ key, error: err instanceof Error ? err.message : String(err) }); },
    );
    return () => ctrl.abort();
  }, [key]);
  return state.key === key ? { ...state, loading: false } : { loading: key !== null };
}

function RepoTree({ files, onSelect }: { files: string[]; onSelect: (path: string) => void }) {
  const onSelectRef = useRef(onSelect);
  useLayoutEffect(() => { onSelectRef.current = onSelect; });
  const known = useRef(new Set(files));
  const { model } = useFileTree({
    paths: files,
    flattenEmptyDirectories: true,
    initialExpansion: 'closed',
    search: true,
    fileTreeSearchMode: 'hide-non-matches',
    onSelectionChange: (selected) => {
      const file = [...selected].reverse().find((p) => known.current.has(p));
      if (file) onSelectRef.current(file);
    },
  });
  return <FileTree model={model} className="oc-diff-fs-files" aria-label="Project files" data-testid="explore-tree" />;
}

function FileBody({ file }: { file: RepoFileContent }) {
  if (file.binary) return <EmptyState>Binary file, not shown.</EmptyState>;
  return (
    <>
      {file.truncated && <EmptyState>Showing the first 1 MiB of this file.</EmptyState>}
      <File file={{ name: file.path, contents: file.content }} options={FILE_OPTIONS} />
    </>
  );
}

// ExploreModal browses every non-ignored file of the repository that
// contains `dir`, rooted at the repo (or worktree) root.
export function ExploreModal({ dir, remoteId, onClose }: { dir: string; remoteId: string; onClose: () => void }) {
  const list = useLoad<RepoFileList>(dir, (signal) => api.repoFiles(dir, remoteId, signal));
  const [selected, setSelected] = useState<string | null>(null);
  const file = useLoad<RepoFileContent>(selected, (signal) => api.repoFile(dir, selected ?? '', remoteId, signal));

  return (
    <Modal onClose={onClose} label="Explore" backdropClassName="oc-diff-fs-backdrop" dialogClassName="oc-diff-fs-modal" dialogTestId="explore-modal">
      <header className="oc-diff-fs-header">
        <h2>Explore</h2>
        <span className="oc-diff-fs-header-count" title={list.data?.root}>
          {list.data && `${shortPath(list.data.root)} · ${list.data.files.length}${list.data.truncated ? '+' : ''} files`}
        </span>
        <IconButton icon="bi-x-lg" label="Close" variant="ghost" size="compact" onClick={onClose} />
      </header>
      <div className="oc-diff-fs-cols">
        {list.data ? <RepoTree files={list.data.files} onSelect={setSelected} />
          : list.error ? <EmptyState className="oc-diff-fs-files">{list.error}</EmptyState>
          : <LoadingState className="oc-diff-fs-files">Loading files…</LoadingState>}
        <div className="oc-diff-fs-diff">
          {file.data ? <FileBody file={file.data} />
            : file.error ? <EmptyState>{file.error}</EmptyState>
            : file.loading ? <LoadingState>Loading…</LoadingState>
            : <EmptyState>Select a file to view it.</EmptyState>}
        </div>
      </div>
    </Modal>
  );
}
