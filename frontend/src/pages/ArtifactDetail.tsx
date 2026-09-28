import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { ArtifactSessionLink } from '../components/ArtifactList';
import { ArtifactPreview } from '../components/ArtifactPreview';
import { ArtifactShareModal } from '../components/ArtifactShareModal';
import { Button } from '../components/Control';
import { ProjectLabel } from '../components/ProjectLabel';
import { MarkdownContent } from '../components/assistant/MarkdownText';
import { artifactsApi, formatBytes, previewKind, useKnownSessionIds, type Artifact } from '../lib/artifactsApi';
import { formatDateTimeShort } from '../lib/format';
import { usePageTitle } from '../lib/headerContext';
import '../components/Artifacts.css';

export function ArtifactDetail() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const known = useKnownSessionIds();
  const [artifact, setArtifact] = useState<Artifact>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [sharing, setSharing] = useState(false);
  usePageTitle(artifact?.title ?? 'Artifact');

  useEffect(() => {
    const ctrl = new AbortController();
    artifactsApi.get(id, ctrl.signal).then(setArtifact, (err) => {
      if (!ctrl.signal.aborted) setError(err instanceof Error ? err.message : 'Could not load artifact.');
    });
    return () => ctrl.abort();
  }, [id]);

  const remove = async () => {
    if (!artifact || !window.confirm(`Delete "${artifact.title}"?`)) return;
    setBusy(true);
    try {
      await artifactsApi.remove(artifact.id);
      navigate('/artifacts');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not delete artifact.');
      setBusy(false);
    }
  };

  if (!artifact) {
    return <main className="artifact-page">{error ? <p role="alert" className="artifact-missing">{error}</p> : <div className="oc-list-loading" role="status"><div className="oc-spinner" />Loading artifact...</div>}<Link to="/artifacts">Back to artifacts</Link></main>;
  }
  const files = artifact.items.filter((it) => it.kind === 'file');
  const links = artifact.items.filter((it) => it.kind === 'link');

  return (
    <main className="artifact-page">
      <header className="artifact-header">
        <div>
          <Link to="/artifacts">Artifacts</Link>
          <h2>{artifact.title}</h2>
          <p className="artifact-muted"><ProjectLabel path={artifact.directory} /> · <ArtifactSessionLink artifact={artifact} known={known} /> · {formatDateTimeShort(Date.parse(artifact.createdAt))}</p>
        </div>
        <div className="artifact-actions">
          <Button type="button" onClick={() => setSharing(true)}><i className="bi bi-share" aria-hidden="true" />Share</Button>
          <Button type="button" className="artifact-delete" disabled={busy} onClick={() => void remove()}><i className="bi bi-trash" aria-hidden="true" />Delete</Button>
        </div>
      </header>
      {error && <p role="alert" className="artifact-missing">{error}</p>}
      {sharing && <ArtifactShareModal artifact={artifact} onClose={() => setSharing(false)} />}
      {artifact.description && <section className="artifact-description"><MarkdownContent text={artifact.description} /></section>}
      {links.length > 0 && (
        <section aria-label="Links"><h3>Links</h3><ul>
          {links.map((l, i) => <li key={i}><a href={l.url} target="_blank" rel="noopener noreferrer">{l.label || l.url}</a></li>)}
        </ul></section>
      )}
      {files.length > 0 && (
        <section aria-label="Files" className="artifact-files"><h3>Files</h3>
          {files.map((f) => (
            <article key={f.url} className="artifact-file" data-testid="artifact-file">
              <header>
                <strong>{f.name}</strong>
                <span className="artifact-muted">{f.mime} · {formatBytes(f.size ?? 0)}</span>
                {previewKind(f) === 'none' && <a href={f.url} target="_blank" rel="noopener noreferrer">Open</a>}
                <a href={`${f.url}?download=1`} download={f.name} aria-label={`Download ${f.name}`}>Download</a>
              </header>
              <ArtifactPreview item={f} />
            </article>
          ))}
        </section>
      )}
    </main>
  );
}
