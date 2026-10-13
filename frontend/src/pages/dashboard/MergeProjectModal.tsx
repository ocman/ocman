import { useState } from 'react';
import { api } from '../../lib/api';
import type { Project } from '../../lib/api';
import { checkoutKey } from '../../lib/projectIdentity';
import { Modal } from '../../components/Modal';
import { ModalHeader } from '../../components/ModalHeader';
import { ModalFooter } from '../../components/ModalFooter';
import { Button, SelectField } from '../../components/Control';
import { InlineAlert } from '../../components/InlineAlert';

export function MergeProjectModal({ source, projects, onClose, onMerged }: {
  source: Project;
  projects: Project[];
  onClose: () => void;
  onMerged: () => void;
}) {
  const [selected, setSelected] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const targets = projects.filter(p => checkoutKey(p.directory, p.remoteId) !== checkoutKey(source.directory, source.remoteId));
  const target = targets.find(p => checkoutKey(p.directory, p.remoteId) === selected);

  async function merge() {
    if (!target || busy) return;
    setBusy(true);
    setError('');
    try {
      await api.mergeProjects({ directory: source.directory, remoteId: source.remoteId },
        { directory: target.directory, remoteId: target.remoteId });
      onMerged();
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to merge projects');
      setBusy(false);
    }
  }

  return (
    <Modal label="Merge project" onClose={onClose} canClose={!busy}>
      <ModalHeader title="Merge project" closeLabel="Close merge dialog" onClose={onClose} canClose={!busy}
        description={<>Move sessions from {source.directory} into the selected project. Sessions keep their working directories.</>} />
      <label htmlFor="merge-project-target">Target project</label>
      <SelectField id="merge-project-target" value={selected} disabled={busy}
        onChange={event => setSelected(event.target.value)}>
        <option value="">Select a project</option>
        {targets.map(p => <option key={checkoutKey(p.directory, p.remoteId)} value={checkoutKey(p.directory, p.remoteId)}>
          {p.directory}{p.remoteName ? ` (${p.remoteName})` : ''}
        </option>)}
      </SelectField>
      {targets.length === 0 && <p>No other projects available.</p>}
      {error && <InlineAlert>{error}</InlineAlert>}
      <ModalFooter label="Merge project actions">
        <Button onClick={onClose} disabled={busy}>Cancel</Button>
        <Button variant="accent" disabled={!target || busy} aria-busy={busy} onClick={() => void merge()}>
          {busy ? 'Merging...' : 'Merge'}
        </Button>
      </ModalFooter>
    </Modal>
  );
}
