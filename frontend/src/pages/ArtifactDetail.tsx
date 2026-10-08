import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { ArtifactContent } from '../components/ArtifactContent';
import { ArtifactSessionLink } from '../components/ArtifactList';
import { ArtifactShareModal } from '../components/ArtifactShareModal';
import { Button, ButtonGroup } from '../components/Control';
import { LoadingState } from '../components/LoadingState';
import { InlineAlert } from '../components/InlineAlert';
import { ProjectLabel } from '../components/ProjectLabel';
import { artifactsApi, useKnownSessionIds, type Artifact } from '../lib/artifactsApi';
import { formatDateTimeShort } from '../lib/format';
import { usePageTitle } from '../lib/headerContext';
import styles from './ArtifactDetail.module.css';

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

  const back = <Button type="button" size="small" variant="ghost" onClick={() => navigate('/artifacts')}><i className="bi bi-arrow-left" aria-hidden="true" />Back to artifacts</Button>;

  if (!artifact) {
    return <main className={styles.page}>{back}{error ? <InlineAlert>{error}</InlineAlert> : <LoadingState>Loading artifact...</LoadingState>}</main>;
  }

  return (
    <main className={styles.page}>
      {back}
      <header className={styles.header}>
        <div>
          <h2>{artifact.title}</h2>
          <p><ProjectLabel path={artifact.directory} /> · <ArtifactSessionLink artifact={artifact} known={known} /> · {formatDateTimeShort(Date.parse(artifact.createdAt))}</p>
        </div>
        <ButtonGroup label="Artifact actions">
          <Button type="button" onClick={() => setSharing(true)}><i className="bi bi-share" aria-hidden="true" />Share</Button>
          <Button type="button" variant="danger" disabled={busy} onClick={() => void remove()}><i className="bi bi-trash" aria-hidden="true" />Delete</Button>
        </ButtonGroup>
      </header>
      {error && <InlineAlert>{error}</InlineAlert>}
      {sharing && <ArtifactShareModal artifact={artifact} onClose={() => setSharing(false)} />}
      <ArtifactContent description={artifact.description} links={artifact.items.filter((it) => it.kind === 'link')}
        files={artifact.items.filter((it) => it.kind === 'file')} downloadHref={(f) => `${f.url}?download=1`} interactiveHref={(f) => `${f.url}/interactive`} />
    </main>
  );
}
