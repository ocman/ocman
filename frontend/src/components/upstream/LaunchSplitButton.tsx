import { useState } from 'react';
import { postHandle, UpstreamApiError } from '../../lib/upstreamApi';
import { useApiStore } from '../../lib/apiStore';

interface LaunchSplitButtonProps {
  directory: string;
  remote: string;
  type: 'pr' | 'issue';
  number: number;
  crossFork: boolean;
}

type Mode = 'session' | 'worktree';

/**
 * LaunchSplitButton renders the "Handle this PR/Issue" control:
 *
 *   [ Handle in new session ▾ ]
 *     ├─ Handle in new session   (default; same project dir)
 *     └─ Handle in new worktree
 *
 * On worktree-mode for a cross-fork PR, the server returns 409
 * `requires_fetch`; we surface a confirmation dialog and re-post
 * with fetchHead=true on Confirm.
 */
export function LaunchSplitButton({
  directory,
  remote,
  type,
  number,
  crossFork,
}: LaunchSplitButtonProps) {
  const [menuOpen, setMenuOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [launched, setLaunched] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirmFetch, setConfirmFetch] = useState<null | { fetchTarget: string }>(null);
  const seedNewSession = useApiStore((s) => s.seedNewSession);

  const run = async (mode: Mode, fetchHead = false) => {
    setBusy(true);
    setError(null);
    setLaunched(false);
    setMenuOpen(false);
    try {
      const res = await postHandle({
        dir: directory,
        remote,
        type,
        number,
        mode,
        fetchHead,
      });
      // For session mode the user stays put; seed the new session into the
      // sidebar list so it shows up immediately instead of waiting for the
      // next 3 s poll. For worktree mode the new tmux session is attachable
      // via the existing UI flow.
      if (mode === 'session' && res.childSessionId) {
        seedNewSession(res.childSessionId, directory, 'opencode', `${type} #${number}`);
      }
      // Brief "Launched" confirmation so the user knows the request landed.
      setLaunched(true);
      window.setTimeout(() => setLaunched(false), 2000);
      console.info('handle launched:', res);
    } catch (err) {
      if (err instanceof UpstreamApiError && err.envelope?.error.code === 'requires_fetch') {
        const ft = err.envelope.error.fetchTarget ?? '';
        setConfirmFetch({ fetchTarget: ft });
      } else {
        setError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      setBusy(false);
    }
  };

  if (confirmFetch) {
    return (
      <div className="oc-upstream-launch oc-upstream-launch-confirm">
        <p>
          This PR is from a fork. Fetch <code>pull/{number}/head</code> into{' '}
          <code>{confirmFetch.fetchTarget}</code> and create a worktree?
        </p>
        <div className="oc-upstream-launch-confirm-buttons">
          <button
            type="button"
            disabled={busy}
            onClick={() => {
              setConfirmFetch(null);
              void run('worktree', true);
            }}
            data-testid="launch-confirm-fetch"
          >
            Confirm
          </button>
          <button type="button" disabled={busy} onClick={() => setConfirmFetch(null)}>
            Cancel
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="oc-upstream-launch" data-testid="launch-split-button">
      <button
        type="button"
        className="oc-upstream-launch-main"
        disabled={busy}
        onClick={() => void run('session')}
        data-testid="launch-default"
      >
        {busy ? 'Launching…' : launched ? 'Launched ✓' : 'Handle in new session'}
      </button>
      <button
        type="button"
        className="oc-upstream-launch-chevron"
        disabled={busy}
        aria-haspopup="menu"
        aria-expanded={menuOpen}
        onClick={() => setMenuOpen((o) => !o)}
        data-testid="launch-menu-toggle"
      >
        ▾
      </button>
      {menuOpen && (
        <ul role="menu" className="oc-upstream-launch-menu" data-testid="launch-menu">
          <li>
            <button
              type="button"
              onClick={() => void run('worktree')}
              data-testid="launch-worktree"
            >
              Handle in new worktree
              {crossFork && <span className="oc-upstream-launch-hint"> (fetches PR ref)</span>}
            </button>
          </li>
        </ul>
      )}
      {error && (
        <div className="oc-upstream-launch-error" role="alert">
          {error}
        </div>
      )}
    </div>
  );
}
