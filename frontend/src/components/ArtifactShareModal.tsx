import { useEffect, useState } from 'react';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import { Button, TextField } from './Control';
import { CopyButton } from './CopyButton';
import { artifactBytes, artifactsApi, formatBytes, type Artifact, type ArtifactShareList } from '../lib/artifactsApi';
import './Artifacts.css';

/** Publishes an artifact to the share relay and lists/revokes its shares. */
export function ArtifactShareModal({ artifact, onClose }: { artifact: Artifact; onClose: () => void }) {
  const [list, setList] = useState<ArtifactShareList>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    const ctrl = new AbortController();
    artifactsApi.shares(artifact.id, ctrl.signal).then(setList, (err) => {
      if (!ctrl.signal.aborted) setError(err instanceof Error ? err.message : 'Could not load shares.');
    });
    return () => ctrl.abort();
  }, [artifact.id]);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError('');
    try { await fn(); } catch (err) { setError(err instanceof Error ? err.message : 'Share request failed.'); } finally { setBusy(false); }
  };
  const create = () => run(async () => {
    const created = await artifactsApi.share(artifact.id);
    setList((l) => l && { ...l, shares: [created, ...l.shares] });
  });
  const revoke = (shareId: string) => run(async () => {
    await artifactsApi.revokeShare(artifact.id, shareId);
    setList((l) => l && { ...l, shares: l.shares.map((s) => (s.id === shareId ? { ...s, revokedAt: Date.now() } : s)) });
  });

  const links = artifact.items.filter((it) => it.kind === 'link');
  const files = artifact.items.filter((it) => it.kind === 'file');
  const total = artifactBytes(artifact);
  const limit = list?.maxShareBytes ?? 0;
  const active = list?.shares.filter((s) => !s.revokedAt) ?? [];

  return (
    <Modal label="Share artifact" dialogTestId="artifact-share-modal" onClose={onClose}>
      <ModalHeader title="Share artifact" closeLabel="Close share dialog" onClose={onClose}
        description="Anyone with the link can view and download this artifact. The relay stores it encrypted." />
      {links.length > 0 && (
        <section aria-label="Exposed links" className="artifact-share-section"><strong>These links will be visible</strong><ul>
          {links.map((l, i) => <li key={i} className="mono">{l.url}</li>)}
        </ul></section>
      )}
      {files.length > 0 && (
        <section aria-label="Files to upload" className="artifact-share-section"><strong>Files to upload</strong><ul>
          {files.map((f, i) => <li key={i}>{f.name} · {formatBytes(f.size ?? 0)}</li>)}
        </ul>
        <p className={limit && total > limit ? 'artifact-error' : 'artifact-muted'} data-testid="artifact-share-size">
          Total {formatBytes(total)}{limit > 0 && ` of the ${formatBytes(limit)} default relay limit`}
        </p></section>
      )}
      {error && <p role="alert" className="artifact-error">{error}</p>}
      {list && !list.relayConfigured && <p className="artifact-error">No share relay is configured.</p>}
      <Button type="button" variant="accent" disabled={busy || !list?.relayConfigured} onClick={() => void create()}><i className="bi bi-link-45deg" aria-hidden="true" />Create share link</Button>
      {active.length > 0 && (
        <ul className="artifact-share-links" aria-label="Share links">
          {active.map((s) => (
            <li key={s.id}>
              <TextField type="text" readOnly value={s.url} aria-label="Share link" onFocus={(e) => e.currentTarget.select()} />
              <CopyButton text={s.url} label="Copy" />
              <Button type="button" variant="danger" disabled={busy} onClick={() => void revoke(s.id)}>Revoke</Button>
            </li>
          ))}
        </ul>
      )}
    </Modal>
  );
}
