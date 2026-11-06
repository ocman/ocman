import { useEffect, useState, useSyncExternalStore } from 'react';
import { Button, TextareaField } from '../../components/Control';
import { InlineAlert } from '../../components/InlineAlert';
import { Modal } from '../../components/Modal';
import { ModalHeader } from '../../components/ModalHeader';
import { ModalFooter } from '../../components/ModalFooter';
import { getConversationDraft } from '../../lib/newConversationDrafts';
import { getDraft, saveDraft, subscribeDraftTexts } from '../../lib/composerDraft';
import { loadBackendDraftConflict, resolveBackendDraftConflict, useBackendConversationDrafts, type BackendConversationDraft } from '../../lib/backendConversationDrafts';
import { newSessionPath } from '../../lib/newSessionPath';
import type { NewConversationProps } from './NewConversation';

type Props = { id: string; error?: string; retry: () => Promise<void>; navigate: NewConversationProps['navigate']; getLocalText?: () => string | undefined };

export function DraftSaveError({ id, error, retry, navigate, getLocalText }: Props) {
  const conflict = useBackendConversationDrafts((state) => state.conflicts[id]);
  const [open, setOpen] = useState(false);
  if (!error && !open) return null;
  return <>
    {error && <InlineAlert onRetry={conflict ? () => { const text = getLocalText?.(); if (text !== undefined) saveDraft(id, text); setOpen(true); } : retry}>Draft not saved on the backend: {error}</InlineAlert>}
    {open && <DraftConflict id={id} onClose={() => setOpen(false)} navigate={navigate} />}
  </>;
}

function DraftConflict({ id, onClose, navigate }: Pick<Props, 'id' | 'navigate'> & { onClose: () => void }) {
  const [local] = useState(() => getConversationDraft(id));
  const text = useSyncExternalStore(subscribeDraftTexts, () => getDraft(id));
  const [server, setServer] = useState<BackendConversationDraft>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let active = true;
    void loadBackendDraftConflict(id).then((draft) => { if (active) setServer(draft); }).catch((cause) => {
      if (active) setError(cause instanceof Error ? cause.message : String(cause));
    });
    return () => { active = false; };
  }, [id, attempt]);
  async function choose(choice: 'local' | 'server') {
    if (!server) return;
    setBusy(true);
    try {
      await resolveBackendDraftConflict(id, server, choice);
      if (choice === 'server') navigate(newSessionPath({ ...server.metadata, draftId: id }), { replace: true });
      onClose();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      setServer(undefined);
    } finally { setBusy(false); }
  }
  return <Modal label="Resolve draft conflict" onClose={onClose} canClose={!busy}>
    <ModalHeader title="Resolve draft conflict" description="Another browser saved a different draft. Nothing changes until you choose a version."
      onClose={onClose} closeLabel="Close conflict" canClose={!busy} />
    <label>My version<TextareaField aria-label="My version" value={text} readOnly rows={5} /></label>
    <details><summary>My selections</summary><TextareaField aria-label="My selections" value={JSON.stringify({ ...local, text: undefined }, null, 2)} readOnly rows={5} /></details>
    {server ? <>
      <label>Server version<TextareaField aria-label="Server version" value={server.metadata.text ?? ''} readOnly rows={5} /></label>
      <details><summary>Server selections</summary><TextareaField aria-label="Server selections" value={JSON.stringify({ ...server.metadata, text: undefined }, null, 2)} readOnly rows={5} /></details>
    </> : !error && <p role="status">Loading server version…</p>}
    {error && <InlineAlert onRetry={() => { setError(''); setAttempt(attempt + 1); }}>{error}</InlineAlert>}
    <ModalFooter label="Resolve draft conflict">
      <Button onClick={onClose} disabled={busy} data-autofocus>Cancel</Button>
      <Button onClick={() => void choose('local')} disabled={!server || busy}>Keep my edits</Button>
      <Button onClick={() => void choose('server')} disabled={!server || busy}>Use server version</Button>
    </ModalFooter>
  </Modal>;
}
