import { useCallback } from 'react';
import { api } from '../lib/api';
import { copyToClipboard } from '../lib/clipboard';
import { Modal } from './Modal';
import { ShareLinkList } from './ShareLinkList';
import { useShareLinks } from '../lib/useShareLinks';
import './ShareExportMenu.css';

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
  const state = useShareLinks(load, () => sessionId);
  const { busy, setLinks, setLoaded, run, flashCopied } = state;

  const handleCreate = () =>
    run(async () => {
      const link = await api.createShareLink(sessionId);
      setLinks((prev) => [link, ...prev]);
      setLoaded(true);
      // Best-effort copy of the freshly minted link.
      await copyToClipboard(link.url);
      flashCopied(link.token);
    }, 'Failed to create share link');

  return (
    <Modal
      backdropClassName="oc-share-modal-backdrop"
      backdropTestId="share-link-backdrop"
      dialogClassName="oc-share-modal"
      dialogTestId="share-link-modal"
      label="Public share link"
      onClose={onClose}
    >
      <div className="oc-share-menu-label">Public share link</div>
      <p className="oc-share-menu-hint">
        Anyone with the link can view this conversation read-only.
      </p>
      <button
        type="button"
        className="oc-share-menu-item oc-share-menu-create"
        onClick={() => void handleCreate()}
        disabled={busy}
        data-testid="share-create-link"
      >
        {busy ? 'Working…' : 'Create share link'}
      </button>

      <ShareLinkList
        state={state}
        emptyText="No active share links."
        urlLabel="Relay share URL"
        copyLabel="Copy relay link"
      />
    </Modal>
  );
}
