import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { artifactsApi, type Artifact, type ArtifactListParams } from '../lib/artifactsApi';
import { formatDateTimeShort } from '../lib/format';
import { useUiStore, type ArtifactsSidebarScope } from '../lib/uiStore';
import { onArtifactCreated } from '../lib/useGlobalEvents';
import { projectRootForDirectory } from '../lib/worktrees';
import { ArtifactPreview } from './ArtifactPreview';
import { Button } from './Control';
import { SegmentedControl } from './SegmentedControl';
import './Artifacts.css';

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
  const [error, setError] = useState('');

  const root = directory ? projectRootForDirectory(directory) : '';
  const params: ArtifactListParams | null = scope === 'session'
    ? (platformId ? { platform: platformId, sessionId, includeDescendants: true } : null)
    : (root ? { directory: root } : null);
  const key = JSON.stringify(params);

  useEffect(() => onArtifactCreated(() => setTick((n) => n + 1)), []);

  useEffect(() => {
    const p: ArtifactListParams | null = JSON.parse(key);
    if (!p) { setArtifacts([]); setCursor(''); return; }
    const ctrl = new AbortController();
    artifactsApi.list(p, ctrl.signal).then(
      (page) => { setArtifacts(page.artifacts ?? []); setCursor(page.nextCursor); setError(''); },
      (err) => { if (!ctrl.signal.aborted) setError(err instanceof Error ? err.message : 'Could not load artifacts.'); },
    );
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
    <div className="artifact-pane" data-testid="artifacts-pane">
      <SegmentedControl<ArtifactsSidebarScope> label="Artifact scope" options={SCOPES} value={scope} onChange={setScope} />
      {error && <p role="alert" className="artifact-missing">{error}</p>}
      {artifacts.length === 0 ? <p className="artifact-muted">No artifacts yet.</p>
        : <ul className="artifact-pane-list">{artifacts.map((a) => <ArtifactRow key={a.id} artifact={a} />)}</ul>}
      {cursor && <Button type="button" disabled={busy} onClick={() => void loadMore()}>Load more</Button>}
    </div>
  );
}

function ArtifactRow({ artifact }: { artifact: Artifact }) {
  const [open, setOpen] = useState(false);
  return (
    <li className="artifact-pane-row" data-testid="artifact-row">
      <button type="button" aria-expanded={open} onClick={() => setOpen((v) => !v)}>
        <i className={`bi bi-chevron-${open ? 'down' : 'right'}`} aria-hidden="true" />
        <span>{artifact.title}</span>
        <span className="artifact-muted">{artifact.items.length} · {formatDateTimeShort(Date.parse(artifact.createdAt))}</span>
      </button>
      {open && (
        <div className="artifact-pane-body">
          <Link to={`/artifacts/${encodeURIComponent(artifact.id)}`}>Open artifact</Link>
          {artifact.items.map((it, i) => it.kind === 'link'
            ? <a key={i} href={it.url} target="_blank" rel="noopener noreferrer">{it.label || it.url}</a>
            : <div key={i}><strong>{it.name}</strong><ArtifactPreview item={it} /></div>)}
        </div>
      )}
    </li>
  );
}
