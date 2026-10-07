import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { File } from '@pierre/diffs/react';
import { FileTree, useFileTree } from '@pierre/trees/react';
import { api } from '../lib/api';
import type { RepoFileContent, RepoFileList } from '../lib/api.types';
import { shortPath } from '../lib/format';
import { FileBrowserModal } from './FileBrowserModal';
import { CheckboxField } from './CheckboxField';
import { EmptyState } from './EmptyState';
import { LoadingState } from './LoadingState';
import { FILE_OPTIONS } from './diffOptions';
import styles from './ExploreModal.module.css';

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
  return <FileTree model={model} className={styles.tree} aria-label="Project files" data-testid="explore-tree" />;
}

function FileBody({ file }: { file: RepoFileContent }) {
  const [imageError, setImageError] = useState(false);
  if (file.mimeType?.startsWith('image/')) {
    if (file.truncated) return <EmptyState>Image exceeds the 10 MiB preview limit.</EmptyState>;
    if (imageError) return <EmptyState>Unable to display this image.</EmptyState>;
    return <div className={styles.image}><img src={`data:${file.mimeType};base64,${file.content}`} alt={file.path} onError={() => setImageError(true)} /></div>;
  }
  if (file.binary) return <EmptyState>Binary file, not shown.</EmptyState>;
  return (
    <>
      {file.truncated && <EmptyState>Showing the first 1 MiB of this file.</EmptyState>}
      <File file={{ name: file.path, contents: file.content }} options={FILE_OPTIONS} />
    </>
  );
}

// ExploreModal browses every non-ignored file of the repository that
// contains `dir`, rooted at the repo (or worktree) root. A toggle adds
// gitignored files.
export function ExploreModal({ dir, remoteId, onClose }: { dir: string; remoteId: string; onClose: () => void }) {
  const [ignored, setIgnored] = useState(false);
  const list = useLoad<RepoFileList>(`${ignored}:${dir}`, (signal) => api.repoFiles(dir, remoteId, signal, ignored));
  const [selected, setSelected] = useState<string | null>(null);
  const file = useLoad<RepoFileContent>(selected, (signal) => api.repoFile(dir, selected ?? '', remoteId, signal, ignored));
  const toggleIgnored = () => { setIgnored((v) => !v); setSelected(null); };

  return (
    <FileBrowserModal title="Explore" onClose={onClose} dialogTestId="explore-modal"
      description={list.data && <span title={list.data.root}>{`${shortPath(list.data.root)} · ${list.data.files.length}${list.data.truncated ? '+' : ''} files`}</span>}
      sidebar={
        <div className={styles.side}>
          <div className={styles.options}>
            <CheckboxField label="Show ignored files" checked={ignored} onChange={toggleIgnored} />
          </div>
          {list.data ? <RepoTree key={String(ignored)} files={list.data.files} onSelect={setSelected} />
            : list.error ? <EmptyState className={styles.tree}>{list.error}</EmptyState>
            : <LoadingState className={styles.tree}>Loading files…</LoadingState>}
        </div>
      }>
      {file.data ? <FileBody key={file.data.path} file={file.data} />
        : file.error ? <EmptyState>{file.error}</EmptyState>
        : file.loading ? <LoadingState>Loading…</LoadingState>
        : <EmptyState>Select a file to view it.</EmptyState>}
    </FileBrowserModal>
  );
}
