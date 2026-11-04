import { useEffect, useState } from 'react';
import { postJSON } from '../../lib/api';
import type { TargetCandidate } from '../../lib/api.types';
import { Button, SubmitButton, TextField } from '../Control';
import { Modal } from '../Modal';
import { ModalHeader } from '../ModalHeader';
import { ModalFooter } from '../ModalFooter';
import styles from './CloneProjectDialog.module.css';

type ClonePlan = { url: string; directory: string };

export function CloneProjectDialog({ machine, suggestedURL, onClose, onSelect }: {
  machine: TargetCandidate; suggestedURL?: string; onClose: () => void;
  onSelect: (target: TargetCandidate) => Promise<void>;
}) {
  const [url, setURL] = useState(suggestedURL || '');
  const [directory, setDirectory] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(!!suggestedURL);
  const [pending, setPending] = useState(false);
  useEffect(() => {
    if (!suggestedURL) return;
    const controller = new AbortController();
    postJSON<ClonePlan>('/api/sessions/plan-clone', { remoteId: machine.remoteId, url: suggestedURL }, { signal: controller.signal })
      .then((plan) => { if (!controller.signal.aborted) { setURL(plan.url); setDirectory(plan.directory); } })
      .catch((err: unknown) => { if (!controller.signal.aborted) setError(err instanceof Error ? err.message : String(err)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [machine.remoteId, suggestedURL]);
  const confirm = async () => {
    setPending(true); setError('');
    try {
      const plan = await postJSON<ClonePlan>('/api/sessions/plan-clone', { remoteId: machine.remoteId, url, directory });
      await onSelect({ ...machine, dir: plan.directory, cloneUrl: plan.url });
      onClose();
    } catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setPending(false); }
  };
  return <Modal label="Clone project on machine" onClose={onClose} canClose={!pending}>
    <ModalHeader title={`Clone project on ${machine.remoteName}`} onClose={onClose} closeLabel="Cancel clone setup" canClose={!pending} />
    <div className={styles.body}>
    <p>This project has no matching checkout on this machine. It will be cloned when you submit your first prompt.</p>
    <p>Local uncommitted changes and unpushed commits will not transfer. Git will use credentials configured on {machine.remoteName}.</p>
    <label className={styles.field}>Repository URL<TextField aria-label="Repository URL" value={url} disabled={loading || pending} onChange={(e) => setURL(e.target.value)} /></label>
    <label className={styles.field}>Destination directory<TextField aria-label="Destination directory" value={directory} placeholder="Leave empty to use this machine's default" disabled={loading || pending} onChange={(e) => setDirectory(e.target.value)} /></label>
    {loading && <p role="status">Checking destination on {machine.remoteName}…</p>}
    {error && <p className={styles.error} role="alert">{error}</p>}
    </div>
    <ModalFooter label="Clone setup actions">
      <Button onClick={onClose} disabled={pending}>Cancel</Button>
      <SubmitButton variant="accent" onClick={confirm} disabled={loading || !url.trim()} pending={pending} pendingLabel="Checking destination…">Use machine and clone on submit</SubmitButton>
    </ModalFooter>
  </Modal>;
}
