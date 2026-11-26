import { useEffect, useState } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { ArtifactContent } from '../components/ArtifactContent';
import { ArtifactSessionLink } from '../components/ArtifactList';
import { ArtifactShareModal } from '../components/ArtifactShareModal';
import { Button, ButtonGroup } from '../components/Control';
import { LoadingState } from '../components/LoadingState';
import { InlineAlert } from '../components/InlineAlert';
import { PageState } from '../components/PageState';
import { ProjectLabel } from '../components/ProjectLabel';
import { artifactsApi, useKnownSessionIds, type Artifact } from '../lib/artifactsApi';
import { formatDateTimeShort } from '../lib/format';
import { usePageTitle } from '../lib/headerContext';
import { PreviewOwnerContext } from '../lib/previews';
import { useConfirm } from '../lib/useConfirm';
import styles from './ArtifactDetail.module.css';

export function ArtifactDetail() {
  const { id = '' } = useParams();
  const remoteId = new URLSearchParams(useLocation().search).get('remoteId') || 'local';
  return <PreviewOwnerContext.Provider value={remoteId}><ArtifactDetailContent key={`${remoteId}/${id}`} id={id} remoteId={remoteId} /></PreviewOwnerContext.Provider>;
}

function ArtifactDetailContent({ id, remoteId }: { id: string; remoteId: string }) {
  const navigate = useNavigate();
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const { search, state } = useLocation();
  const listURL = `/artifacts${search}`;
  const known = useKnownSessionIds();
  const [artifact, setArtifact] = useState<Artifact>();
  const [error, setError] = useState('');
  const [loadAttempt, setLoadAttempt] = useState(0);
  const [busy, setBusy] = useState(false);
  const [sharing, setSharing] = useState(false);
  usePageTitle(artifact?.title ?? 'Artifact');

  useEffect(() => {
    const ctrl = new AbortController();
    artifactsApi.get(id, ctrl.signal, remoteId).then(setArtifact, (err) => {
      if (!ctrl.signal.aborted) setError(err instanceof Error ? err.message : 'Could not load artifact.');
    });
    return () => ctrl.abort();
  }, [id, remoteId, loadAttempt]);

  const retry = () => {
    setError('');
    setLoadAttempt((attempt) => attempt + 1);
  };

  const remove = async () => {
    if (!artifact || remoteId !== 'local' || busy) return;
    if (!await confirm({
      title: `Delete "${artifact.title}"?`,
      description: 'This permanently removes the artifact and revokes any live share links.',
      confirmLabel: 'Delete artifact',
      destructive: true,
    })) return;
    setError('');
    setBusy(true);
    try {
      await artifactsApi.remove(artifact.id);
      void queryClient.invalidateQueries({ queryKey: ['artifacts'] });
      void queryClient.invalidateQueries({ queryKey: ['artifact-stats'] });
      navigate(listURL, { replace: true });
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not delete artifact.');
      setBusy(false);
    }
  };

  const backToArtifacts = () => {
    if (state?.fromArtifactsList === true) navigate(-1);
    else navigate(listURL, { replace: true });
  };
  const back = <Button type="button" size="small" variant="ghost" onClick={backToArtifacts}><i className="bi bi-arrow-left" aria-hidden="true" />Back to artifacts</Button>;

  if (!artifact) {
    return <main className={styles.page}>{back}{error ? <PageState error title="Artifact is unavailable" description={error} onRetry={retry} /> : <LoadingState>Loading artifact...</LoadingState>}</main>;
  }

  return (
    <main className={styles.page}>
      {back}
      <header className={styles.header}>
        <div>
          <h2>{artifact.title}</h2>
          <p><ProjectLabel path={artifact.directory} /> · <ArtifactSessionLink artifact={artifact} known={known} /> · {formatDateTimeShort(Date.parse(artifact.createdAt))}</p>
        </div>
        {remoteId === 'local' && <ButtonGroup label="Artifact actions">
          <Button type="button" onClick={() => setSharing(true)}><i className="bi bi-share" aria-hidden="true" />Share</Button>
          <Button type="button" variant="danger" disabled={busy} aria-busy={busy} onClick={() => void remove()}><i className="bi bi-trash" aria-hidden="true" />{busy ? 'Deleting...' : 'Delete'}</Button>
        </ButtonGroup>}
      </header>
      {error && <InlineAlert>{error}</InlineAlert>}
      {sharing && <ArtifactShareModal artifact={artifact} onClose={() => setSharing(false)} />}
      <ArtifactContent description={artifact.description} links={artifact.items.filter((it) => it.kind === 'link')}
        files={artifact.items.filter((it) => it.kind === 'file')} downloadHref={(f) => `${f.url}?download=1`} interactiveHref={(f) => `${f.url}/interactive`} />
    </main>
  );
}
