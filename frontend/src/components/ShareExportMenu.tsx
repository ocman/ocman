import { useCallback } from 'react';
import { api } from '../lib/api';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import { ModalFooter } from './ModalFooter';
import { Button } from './Control';
import { ShareLinkList } from './ShareLinkList';
import { useShareLinks } from '../lib/useShareLinks';

interface ShareLinkModalProps {
  sessionId: string;
  onClose: () => void;
}

/**
 * ShareLinkModal manages public read-only share links for a session:
 * create / list / copy / revoke. The link is unauthenticated — anyone
 * with the URL can view the conversation. Rendered as a modal dialog
 * opened from the header actions menu.
 */
export function ShareLinkModal({ sessionId, onClose }: ShareLinkModalProps) {
  const load = useCallback(() => api.listShareLinks(sessionId), [sessionId]);
  const state = useShareLinks(load, (link) => api.revokeShareLink(sessionId, link.token));
  const { busy, setLinks, setLoaded, run } = state;

  const handleCreate = () =>
    run(async () => {
      const link = await api.createShareLink(sessionId);
      setLinks((prev) => [link, ...prev]);
      setLoaded(true);
      // Best-effort copy of the freshly minted link.
      await state.copy(link);
    }, 'Failed to create share link');

  return (
    <Modal
      backdropTestId="share-link-backdrop"
      dialogTestId="share-link-modal"
      label="Public share link"
      onClose={onClose}
      canClose={!busy}
    >
      <ModalHeader title="Public share link" closeLabel="Close share link dialog" onClose={onClose} canClose={!busy}
        description="Anyone with the link can view this conversation read-only." />
      <Button
        type="button"
        variant="accent"
        onClick={() => void handleCreate()}
        disabled={busy}
        aria-busy={busy}
        data-testid="share-create-link"
      >
        {busy ? 'Working…' : 'Create share link'}
      </Button>

      <ShareLinkList
        state={state}
        emptyText="No active share links."
        urlLabel="Relay share URL"
        copyLabel="Copy relay link"
      />
      <ModalFooter label="Share link dialog actions"><Button type="button" disabled={busy} onClick={onClose}>Close</Button></ModalFooter>
    </Modal>
  );
}
