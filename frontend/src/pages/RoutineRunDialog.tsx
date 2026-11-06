import { useState, type FormEvent } from 'react';
import { Button } from '../components/Control';
import { InlineAlert } from '../components/InlineAlert';
import { Modal } from '../components/Modal';
import { ModalHeader } from '../components/ModalHeader';
import { ModalFooter } from '../components/ModalFooter';
import { SearchSelect } from '../components/SearchSelect';
import { api, type Routine } from '../lib/api';
import { useProjects } from '../lib/queries';

export function RoutineRunDialog({ routine, onClose, onStarted }: { routine: Routine; onClose: () => void; onStarted: () => void }) {
  const projects = useProjects();
  const [directory, setDirectory] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const options = (projects.data ?? []).filter((project) => !project.archived && (project.remoteId || 'local') === (routine.remoteId || 'local')).map((project) => ({ value: project.directory, label: project.directory }));
  const submit = async (event: FormEvent) => {
    event.preventDefault(); setBusy(true); setError('');
    try { await api.routines.run(routine.id, directory); onStarted(); }
    catch (err) { setError(err instanceof Error ? err.message : 'Could not run routine.'); }
    finally { setBusy(false); }
  };
  return <Modal label="Run routine" onClose={onClose} canClose={!busy}>
    <ModalHeader title={`Run ${routine.name}`} onClose={onClose} canClose={!busy} closeLabel="Close run routine" />
    <form onSubmit={submit}>
      {(error || projects.error) && <InlineAlert>{error || projects.error?.message}</InlineAlert>}
      <SearchSelect ariaLabel="Run project" searchLabel="Search projects" placeholder={projects.isPending ? 'Loading projects...' : 'Select a project'} value={directory} options={options} disabled={busy || projects.isPending} onChange={setDirectory} />
      <ModalFooter label="Run routine actions"><Button type="button" disabled={busy} onClick={onClose}>Cancel</Button><Button type="submit" variant="accent" disabled={busy || !directory} aria-busy={busy}>Run now</Button></ModalFooter>
    </form>
  </Modal>;
}
