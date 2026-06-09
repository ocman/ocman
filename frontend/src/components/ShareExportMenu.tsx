import { useCallback, useEffect, useRef, useState } from 'react';
import { api, sessionExportMarkdownUrl, type ShareLink } from '../lib/api';
import './ShareExportMenu.css';

interface ShareExportMenuProps {
  sessionId: string;
}

/**
 * ShareExportMenu renders the conversation "Export / Share" dropdown in
 * the session header. It offers three things:
 *
 *  - Download Markdown  → navigates to the auth-gated export.md endpoint
 *    (the browser handles the download via Content-Disposition).
 *  - Print / Save as PDF → window.print(); the print stylesheet hides
 *    the chrome so the browser's "Save as PDF" produces a clean export.
 *  - Share link → creates / lists / copies / revokes public read-only
 *    links. The link is unauthenticated: anyone with the URL can view.
 *
 * Share links are loaded lazily the first time the menu is opened so
 * the session header stays cheap for the common case.
 */
export function ShareExportMenu({ sessionId }: ShareExportMenuProps) {
  const [open, setOpen] = useState(false);
  const [links, setLinks] = useState<ShareLink[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  // Close on outside click / Escape so the menu behaves like a normal
  // popover.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

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

  const toggle = useCallback(() => {
    setOpen((o) => {
      const next = !o;
      if (next && !loaded) void loadLinks();
      return next;
    });
  }, [loaded, loadLinks]);

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

  const handlePrint = useCallback(() => {
    setOpen(false);
    // Defer so the menu unmounts before the print dialog snapshots the
    // page.
    window.setTimeout(() => window.print(), 50);
  }, []);

  return (
    <div className="oc-share-menu" ref={containerRef} data-testid="share-export-menu">
      <button
        type="button"
        className="session-sidebar-new"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Export or share conversation"
        title="Export / Share conversation"
        onClick={toggle}
        data-testid="share-export-toggle"
      >
        ⤴
      </button>
      {open && (
        <div role="menu" className="oc-share-menu-popover" data-testid="share-export-popover">
          <div className="oc-share-menu-section">
            <a
              className="oc-share-menu-item"
              role="menuitem"
              href={sessionExportMarkdownUrl(sessionId)}
              download={`conversation-${sessionId}.md`}
              onClick={() => setOpen(false)}
              data-testid="share-download-md"
            >
              Download Markdown
            </a>
            <button
              type="button"
              role="menuitem"
              className="oc-share-menu-item"
              onClick={handlePrint}
              data-testid="share-print-pdf"
            >
              Print / Save as PDF
            </button>
          </div>

          <div className="oc-share-menu-divider" />

          <div className="oc-share-menu-section">
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
      )}
    </div>
  );
}

// copyToClipboard writes text to the clipboard, falling back to a
// hidden textarea + execCommand when the async Clipboard API is
// unavailable (non-secure contexts, older browsers). Returns whether
// the copy succeeded.
async function copyToClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // fall through to the legacy path
  }
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}
