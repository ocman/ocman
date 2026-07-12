import { useCallback, useEffect, useState } from 'react';
import { api, type ShareLink } from '../lib/api';
import { copyToClipboard } from '../lib/clipboard';
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
  const [links, setLinks] = useState<ShareLink[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);

  // Close on Escape.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  const loadLinks = useCallback(async () => {
    setError(null);
    try {
      const list = await api.listShareLinks(sessionId);
      setLinks(list);
      setLoaded(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load share links');
    }
  }, [sessionId]);

  useEffect(() => {
    void loadLinks();
  }, [loadLinks]);

  const handleCreate = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      const link = await api.createShareLink(sessionId);
      setLinks((prev) => [link, ...prev]);
      setLoaded(true);
      // Best-effort copy of the freshly minted link.
      await copyToClipboard(link.url);
      setCopied(link.token);
      window.setTimeout(() => setCopied((c) => (c === link.token ? null : c)), 2000);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create share link');
    } finally {
      setBusy(false);
    }
  }, [sessionId]);

  const handleCopy = useCallback(async (link: ShareLink) => {
    const ok = await copyToClipboard(link.url);
    if (ok) {
      setCopied(link.token);
      window.setTimeout(() => setCopied((c) => (c === link.token ? null : c)), 2000);
    } else {
      setError('Could not copy to clipboard');
    }
  }, []);

  const handleRevoke = useCallback(async (link: ShareLink) => {
    setBusy(true);
    setError(null);
    try {
      await api.revokeShareLink(sessionId, link.token);
      setLinks((prev) => prev.filter((l) => l.token !== link.token));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to revoke share link');
    } finally {
      setBusy(false);
    }
  }, [sessionId]);

  return (
    <div className="oc-share-modal-backdrop" data-testid="share-link-backdrop" onClick={onClose}>
      <div
        className="oc-share-modal"
        role="dialog"
        aria-modal="true"
        aria-label="Public share link"
        onClick={(e) => e.stopPropagation()}
        data-testid="share-link-modal"
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

        {error && (
          <div className="oc-share-menu-error" role="alert">
            {error}
          </div>
        )}

        {loaded && links.length === 0 && (
          <div className="oc-share-menu-empty">No active share links.</div>
        )}

        {links.length > 0 && (
          <ul className="oc-share-menu-links">
            {links.map((link) => (
              <li key={link.token} className="oc-share-menu-link">
                <input
                  type="text"
                  readOnly
                  value={link.url}
                  className="oc-share-menu-url"
                  aria-label="Share URL"
                  onFocus={(e) => e.currentTarget.select()}
                />
                <div className="oc-share-menu-link-actions">
                  <button
                    type="button"
                    onClick={() => void handleCopy(link)}
                    data-testid="share-copy-link"
                  >
                    {copied === link.token ? 'Copied!' : 'Copy'}
                  </button>
                  <button
                    type="button"
                    className="oc-share-menu-revoke"
                    onClick={() => void handleRevoke(link)}
                    disabled={busy}
                    data-testid="share-revoke-link"
                  >
                    Revoke
                  </button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
