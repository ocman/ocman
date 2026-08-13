import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type GlobalShareLink, type RelaySource } from '../lib/api';
import { copyToClipboard } from '../lib/clipboard';
import { relativeTime } from '../lib/format';
import { SettingRow, SettingToggle } from './SettingRow';
import { useSettingSave } from '../lib/useSaveStatus';

/**
 * SharingSettings renders the master "allow sharing" toggle plus a
 * global list of every active public share link, so links can be found,
 * inspected (jump to the session), copied, and revoked from one place.
 *
 * Sharing is on by default; disabling it stops new links from being
 * minted but leaves existing links active until revoked here.
 */
/** How each relay source is described in the UI. */
const RELAY_SOURCE_LABELS: Record<Exclude<RelaySource, ''>, string> = {
  flag: '-relay-url flag',
  env: 'OCMAN_RELAY_URL environment variable',
  builtin: 'built-in default',
};

export function SharingSettings() {
  const [enabled, setEnabled] = useState(true);
  const [relayUrl, setRelayUrl] = useState('');
  const [relaySource, setRelaySource] = useState<RelaySource>('');
  const [links, setLinks] = useState<GlobalShareLink[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  const sharingSave = useSettingSave();

  const load = useCallback(async () => {
    setError(null);
    try {
      const [sharing, list] = await Promise.all([
        api.getSharingEnabled(),
        api.listAllShares(),
      ]);
      setEnabled(sharing.enabled);
      setRelayUrl(sharing.relayUrl);
      setRelaySource(sharing.relaySource);
      setLinks(list);
      setLoaded(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load sharing settings');
      setLoaded(true);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const handleToggle = useCallback(async (want: boolean) => {
    setEnabled(want); // optimistic
    setError(null);
    try {
      await api.setSharingEnabled(want);
    } catch (err) {
      setEnabled(!want); // revert
      setError(err instanceof Error ? err.message : 'Failed to save setting');
      throw err; // let SettingToggle surface the failure indicator
    }
  }, []);

  const handleCopy = useCallback(async (link: GlobalShareLink) => {
    const ok = await copyToClipboard(link.url);
    if (ok) {
      setCopied(link.token);
      window.setTimeout(() => setCopied((c) => (c === link.token ? null : c)), 2000);
    } else {
      setError('Could not copy to clipboard');
    }
  }, []);

  const handleRevoke = useCallback(async (link: GlobalShareLink) => {
    setBusy(true);
    setError(null);
    try {
      await api.revokeShareLink(link.sessionId, link.token);
      setLinks((prev) => prev.filter((l) => l.token !== link.token));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to revoke share link');
    } finally {
      setBusy(false);
    }
  }, []);

  return (
    <div data-testid="sharing-settings">
      <SettingRow
        label="Allow public sharing"
        desc="Let sessions be shared via public, read-only links. When off, no new share links can be created; existing links keep working until revoked below."
      >
        <SettingToggle
          testId="sharing-toggle"
          ariaLabel="Allow public sharing"
          checked={enabled}
          save={sharingSave}
          onSave={(next) => handleToggle(next)}
        />
      </SettingRow>

      <SettingRow
        label="Share relay"
        desc={
          relayUrl
            ? <>Conversations shared from this instance are stored on this relay,
                encrypted, so they can be opened from another machine. Set by the{' '}
                {RELAY_SOURCE_LABELS[relaySource as Exclude<RelaySource, ''>] ?? 'command line'};
                restart ocman to change it.</>
            : <>No relay configured, so share links only work on this machine. Start
                ocman with <code>-relay-url</code> (or set{' '}
                <code>OCMAN_RELAY_URL</code>) to share across machines.</>
        }
      >
        <output className="mono" data-testid="sharing-relay-url">
          {relayUrl || 'Not configured'}
        </output>
      </SettingRow>

      <SettingRow
        block
        label="Shared sessions"
        desc={<>Every active public share link. Open the session to inspect it, or
          revoke a link to make it stop working immediately.</>}
      >
        {error && (
          <div className="oc-share-menu-error" role="alert">{error}</div>
        )}
        {loaded && links.length === 0 && (
          <div className="oc-share-menu-empty">No shared sessions.</div>
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
                  <Link to={`/session/${encodeURIComponent(link.sessionId)}`} className="vscode-btn">
                    Inspect
                  </Link>
                  <button type="button" onClick={() => void handleCopy(link)}>
                    {copied === link.token ? 'Copied!' : 'Copy'}
                  </button>
                  <button
                    type="button"
                    className="oc-share-menu-revoke"
                    onClick={() => void handleRevoke(link)}
                    disabled={busy}
                  >
                    Revoke
                  </button>
                </div>
                <div className="settings-row-desc" style={{ marginTop: 4 }}>
                  Shared {relativeTime(link.createdAt)}
                </div>
              </li>
            ))}
          </ul>
        )}
      </SettingRow>
    </div>
  );
}
