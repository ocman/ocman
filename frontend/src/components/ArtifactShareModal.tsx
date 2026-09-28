import { useEffect, useState } from 'react';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import { Button } from './Control';
import { artifactBytes, artifactsApi, formatBytes, type Artifact, type ArtifactShareList } from '../lib/artifactsApi';
import { copyToClipboard } from '../lib/clipboard';
import './MachinePickerModal.css';

/** Publishes an artifact to the share relay and lists/revokes its shares. */
export function ArtifactShareModal({ artifact, onClose }: { artifact: Artifact; onClose: () => void }) {
  const [list, setList] = useState<ArtifactShareList>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [copied, setCopied] = useState('');

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
  const copy = async (id: string, url: string) => {
    if (await copyToClipboard(url)) setCopied(id);
    else setError('Could not copy to clipboard.');
  };

  const links = artifact.items.filter((it) => it.kind === 'link');
  const files = artifact.items.filter((it) => it.kind === 'file');
  const total = artifactBytes(artifact);
  const limit = list?.maxShareBytes ?? 0;
  const active = list?.shares.filter((s) => !s.revokedAt) ?? [];

  return (
    <Modal label="Share artifact" backdropClassName="machine-picker-backdrop" dialogClassName="machine-picker" dialogTestId="artifact-share-modal" onClose={onClose}>
      <ModalHeader title="Share artifact" closeLabel="Close share dialog" onClose={onClose}
        description="Anyone with the link can view and download this artifact. The relay stores it encrypted." />
      {links.length > 0 && (
        <section aria-label="Exposed links"><strong>These links will be visible:</strong><ul>
          {links.map((l, i) => <li key={i} className="mono">{l.url}</li>)}
        </ul></section>
      )}
      {files.length > 0 && (
        <section aria-label="Files to upload"><strong>Files to upload:</strong><ul>
          {files.map((f, i) => <li key={i}>{f.name} · {formatBytes(f.size ?? 0)}</li>)}
        </ul>
        <p className={limit && total > limit ? 'artifact-missing' : 'artifact-muted'} data-testid="artifact-share-size">
          Total {formatBytes(total)}{limit > 0 && ` of the ${formatBytes(limit)} default relay limit`}
        </p></section>
      )}
      {error && <p role="alert" className="artifact-missing">{error}</p>}
      {list && !list.relayConfigured && <p className="artifact-missing">No share relay is configured.</p>}
      <Button type="button" disabled={busy || !list?.relayConfigured} onClick={() => void create()}>Create share link</Button>
      {active.length > 0 && (
        <ul className="machine-picker-list" aria-label="Share links">
          {active.map((s) => (
            <li key={s.id}>
              <input type="text" readOnly value={s.url} aria-label="Share link" onFocus={(e) => e.currentTarget.select()} />
              <button type="button" onClick={() => void copy(s.id, s.url)}>{copied === s.id ? 'Copied!' : 'Copy'}</button>
              <button type="button" disabled={busy} onClick={() => void revoke(s.id)}>Revoke</button>
            </li>
          ))}
        </ul>
      )}
    </Modal>
  );
}
