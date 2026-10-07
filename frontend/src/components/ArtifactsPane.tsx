import { useCallback, useEffect, useState } from 'react';
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
import styles from './ArtifactsPane.module.css';

const SCOPES = [
  { value: 'session', label: 'This session' },
  { value: 'project', label: 'This project' },
] as const;

interface ArtifactsPaneProps {
  sessionId: string;
  platformId: string | undefined;
  directory: string | undefined;
}

/** Right-panel Artifacts pane: this session (with subagents) or the whole project. */
export function ArtifactsPane({ sessionId, platformId, directory }: ArtifactsPaneProps) {
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

  useEffect(() => onArtifactCreated(() => setTick((n) => n + 1)), []);

  useEffect(() => {
    const p: ArtifactListParams | null = JSON.parse(key);
    if (!p) { setArtifacts([]); setCursor(''); setError(''); setLoading(false); return; }
    const ctrl = new AbortController();
    setLoading(true);
    artifactsApi.list(p, ctrl.signal).then(
      (page) => { setArtifacts(page.artifacts ?? []); setCursor(page.nextCursor); setError(''); },
      (err) => { if (!ctrl.signal.aborted) setError(err instanceof Error ? err.message : 'Could not load artifacts.'); },
    ).finally(() => { if (!ctrl.signal.aborted) setLoading(false); });
    return () => ctrl.abort();
  }, [key, tick]);

  const loadMore = useCallback(async () => {
    setBusy(true);
    try {
      const page = await artifactsApi.list({ ...JSON.parse(key), cursor });
      setArtifacts((prev) => [...prev, ...(page.artifacts ?? [])]);
      setCursor(page.nextCursor);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not load artifacts.');
    } finally {
      setBusy(false);
    }
  }, [key, cursor]);

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
