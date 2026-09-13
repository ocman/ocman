import type { TmuxSession } from '../../lib/api';
import { sessionExportMarkdownUrl } from '../../lib/api';
import { shortPath } from '../../lib/format';

export interface SessionActionsMenuProps {
  sessionId: string;
  tmuxAvailable: boolean;
  matchingTmuxSession: TmuxSession | undefined;
  /** Composer is live; hides the "Launch opencode" item. */
  portAvailable: boolean;
  liveConnectionHint: string | undefined;
  launchingOpencode: boolean;
  onNewSession: () => void;
  onShare: () => void;
  onTmuxSwitch: (e: React.MouseEvent, tmuxSessionName: string) => void;
  onLaunchOpencode: () => void;
  onOpenVSCode: () => void;
}

/** Closes the enclosing `<details>` menu before running the action. */
function closeMenu(e: React.MouseEvent<HTMLElement>) {
  (e.currentTarget.closest('details') as HTMLDetailsElement | null)?.removeAttribute('open');
}

/** The "⋯" session actions menu mounted into the header slot. */
export function SessionActionsMenu({
  sessionId,
  tmuxAvailable,
  matchingTmuxSession,
  portAvailable,
  liveConnectionHint,
  launchingOpencode,
  onNewSession,
  onShare,
  onTmuxSwitch,
  onLaunchOpencode,
  onOpenVSCode,
}: SessionActionsMenuProps) {
  return (
    <details className="oc-project-menu header-actions-menu">
      <summary
        className="oc-project-menu-trigger"
        title="Session actions"
        aria-label="Session actions"
      >⋯</summary>
      <div className="oc-project-menu-list" role="menu">
        <button
          type="button"
          role="menuitem"
          className="oc-project-menu-item"
          onClick={(e) => { closeMenu(e); onNewSession(); }}
          title="New session"
        >New session</button>

        <div className="oc-project-menu-separator" role="separator" />

        <a
          role="menuitem"
          className="oc-project-menu-item"
          href={sessionExportMarkdownUrl(sessionId)}
          download={`conversation-${sessionId}.md`}
          onClick={closeMenu}
        >Download Markdown</a>
        <button
          type="button"
          role="menuitem"
          className="oc-project-menu-item"
          onClick={(e) => {
            closeMenu(e);
            // Defer so the menu unmounts before print snapshots the page.
            window.setTimeout(() => window.print(), 50);
          }}
        >Print / Save as PDF</button>
        <button
          type="button"
          role="menuitem"
          className="oc-project-menu-item"
          onClick={(e) => { closeMenu(e); onShare(); }}
        >Share link…</button>

        <div className="oc-project-menu-separator" role="separator" />

        {tmuxAvailable && matchingTmuxSession && (
          <button
            type="button"
            role="menuitem"
            className="oc-project-menu-item"
            onClick={(e) => { closeMenu(e); onTmuxSwitch(e, matchingTmuxSession.name); }}
            title={`Switch tmux to ${shortPath(matchingTmuxSession.name)} (T)`}
          >Switch tmux</button>
        )}
        {tmuxAvailable && !portAvailable && liveConnectionHint && (
          <button
            type="button"
            role="menuitem"
            className="oc-project-menu-item"
            onClick={(e) => { closeMenu(e); onLaunchOpencode(); }}
            disabled={launchingOpencode}
            title="Launch opencode --port 0 in a new tmux window"
          >{launchingOpencode ? 'Launching…' : 'Launch opencode'}</button>
        )}
        <button
          type="button"
          role="menuitem"
          className="oc-project-menu-item"
          onClick={(e) => { closeMenu(e); onOpenVSCode(); }}
          title="Open in VS Code (V)"
        >Open in VS Code</button>
      </div>
    </details>
  );
}
