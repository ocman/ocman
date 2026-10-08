import { useCallback, useEffect, useRef, useState } from 'react';
import { artifactsApi, type Artifact, type ArtifactListParams } from '../lib/artifactsApi';
import { formatDateTimeShort } from '../lib/format';
import { useUiStore, type ArtifactsSidebarScope } from '../lib/uiStore';
import { onArtifactCreated } from '../lib/useGlobalEvents';
import { projectRootForDirectory } from '../lib/worktrees';
import { ArtifactPreview } from './ArtifactPreview';
import { Button, RouteButton } from './Control';
import { EmptyState } from './EmptyState';
import { InlineAlert } from './InlineAlert';
import { LoadingState } from './LoadingState';
import { SegmentedControl } from './SegmentedControl';
import { useDocumentVisible } from '../lib/usePanelVisible';
import { withDeadline } from '../lib/coalescedRefresh';
import styles from './ArtifactsPane.module.css';

const SCOPES = [
  { value: 'session', label: 'This session' },
  { value: 'project', label: 'This project' },
] as const;
const PAGE_TIMEOUT_MS = 15_000;

interface ArtifactsPaneProps {
  sessionId: string;
  platformId: string | undefined;
  directory: string | undefined;
}

/** Right-panel Artifacts pane: this session (with subagents) or the whole project. */
export function ArtifactsPane({ sessionId, platformId, directory }: ArtifactsPaneProps) {
  const visible = useDocumentVisible();
  const scope = useUiStore((s) => s.artifactsSidebarScope);
  const setScope = useUiStore((s) => s.setArtifactsSidebarScope);
  const [artifacts, setArtifacts] = useState<Artifact[]>([]);
  const [cursor, setCursor] = useState('');
  const [tick, setTick] = useState(0);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const root = directory ? projectRootForDirectory(directory) : '';
  const params: ArtifactListParams | null = scope === 'session'
    ? (platformId ? { platform: platformId, sessionId, includeDescendants: true } : null)
    : (root ? { directory: root } : null);
  const key = JSON.stringify(params);
  const lastKey = useRef(key);
  const loadedPages = useRef(1);
  const paused = useRef(false);
  const work = useRef<Promise<void>>(Promise.resolve());
  const cursorRef = useRef('');
  const mounted = useRef(true);
  const paginationAbort = useRef<AbortController | null>(null);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; paginationAbort.current?.abort(); }; }, []);

  useEffect(() => onArtifactCreated(() => setTick((n) => n + 1)), []);

  useEffect(() => {
    if (lastKey.current !== key) {
      paginationAbort.current?.abort();
      paginationAbort.current = null;
      work.current = Promise.resolve();
      loadedPages.current = 1;
      cursorRef.current = '';
      paused.current = false;
      lastKey.current = key;
      const reset = () => { setArtifacts([]); setCursor(''); setBusy(false); setError(''); };
      reset();
    }
    if (!visible) { paused.current = true; return; }
    const p: ArtifactListParams | null = JSON.parse(key);
    if (!p) { setArtifacts([]); setCursor(''); setError(''); setLoading(false); return; }
    lastKey.current = key;
    const ctrl = new AbortController();
    // Refresh the pages the user already opened without dropping their rows
    // (and expanded previews) just because the document was backgrounded.
    const refresh = work.current.catch(() => {}).then(async () => {
      if (!mounted.current || ctrl.signal.aborted || document.hidden || lastKey.current !== key) return;
      setLoading(true);
      const pageCount = paused.current ? loadedPages.current : 1;
      const rows: Artifact[] = [];
      let nextCursor = '';
      let pages = 0;
      for (let i = 0; i < pageCount; i++) {
        const page = await withDeadline(PAGE_TIMEOUT_MS, (signal) => artifactsApi.list({ ...p, ...(nextCursor ? { cursor: nextCursor } : {}) }, signal), ctrl.signal);
        rows.push(...page.artifacts ?? []);
        nextCursor = page.nextCursor;
        pages += 1;
        if (!nextCursor) break;
      }
      if (ctrl.signal.aborted) return;
      paused.current = false;
      loadedPages.current = pages;
      cursorRef.current = nextCursor;
      setArtifacts(rows); setCursor(nextCursor); setError('');
    });
    work.current = refresh;
    refresh.catch(
      (err) => { if (!ctrl.signal.aborted) setError(err instanceof Error ? err.message : 'Could not load artifacts.'); },
    ).finally(() => { if (!ctrl.signal.aborted) setLoading(false); });
    return () => ctrl.abort();
  }, [key, tick, visible]);

  const loadMore = useCallback(async () => {
    setBusy(true);
    const controller = new AbortController();
    paginationAbort.current = controller;
    const more = work.current.catch(() => {}).then(async () => {
      if (!mounted.current || controller.signal.aborted || document.hidden || lastKey.current !== key || !cursorRef.current) return;
      const page = await withDeadline(PAGE_TIMEOUT_MS, (signal) => artifactsApi.list({ ...JSON.parse(key), cursor: cursorRef.current }, signal), controller.signal);
      if (!mounted.current || controller.signal.aborted || lastKey.current !== key) return;
      loadedPages.current += 1;
      cursorRef.current = page.nextCursor;
      setArtifacts((prev) => [...prev, ...(page.artifacts ?? [])]);
      setCursor(page.nextCursor);
    });
    work.current = more;
    try {
      await more;
    } catch (err) {
      if (!controller.signal.aborted && lastKey.current === key) setError(err instanceof Error ? err.message : 'Could not load artifacts.');
    } finally {
      if (paginationAbort.current === controller) paginationAbort.current = null;
      if (mounted.current && lastKey.current === key) setBusy(false);
    }
  }, [key]);

  return (
    <div className={styles.pane} data-testid="artifacts-pane">
      <SegmentedControl<ArtifactsSidebarScope> label="Artifact scope" options={SCOPES} value={scope} onChange={setScope} />
      {error && <InlineAlert onRetry={() => setTick((n) => n + 1)} retrying={loading || busy}>{error}</InlineAlert>}
      {loading && artifacts.length === 0 ? <LoadingState>Loading artifacts…</LoadingState>
        : artifacts.length > 0 ? <ul className={styles.list} aria-label="Artifacts">{artifacts.map((a) => <ArtifactRow key={a.id} artifact={a} />)}</ul>
        : !error && <EmptyState>No artifacts yet.</EmptyState>}
      {cursor && <Button type="button" disabled={busy || loading} aria-busy={busy} onClick={() => void loadMore()}>Load more</Button>}
    </div>
  );
}

function ArtifactRow({ artifact }: { artifact: Artifact }) {
  const [open, setOpen] = useState(false);
  return (
    <li className={styles.row} data-testid="artifact-row">
      <Button type="button" variant="ghost" size="small" className={styles.summary} aria-expanded={open} onClick={() => setOpen((v) => !v)}>
        <i className={`bi bi-chevron-${open ? 'down' : 'right'} ${styles.chevron}`} aria-hidden="true" />
        <span className={styles.title}>{artifact.title}</span>
        <span className={styles.metadata}>{artifact.items.length} items · {formatDateTimeShort(Date.parse(artifact.createdAt))}</span>
      </Button>
      {open && (
        <div className={styles.body}>
          <RouteButton to={`/artifacts/${encodeURIComponent(artifact.id)}`} size="small">Open artifact</RouteButton>
          {artifact.items.map((it, i) => it.kind === 'link'
            ? <a key={i} href={it.url} target="_blank" rel="noopener noreferrer">{it.label || it.url}</a>
            : <div key={i}><strong>{it.name}</strong><ArtifactPreview item={it} /></div>)}
        </div>
      )}
    </li>
  );
}
