import { useRef } from 'react';
import type { TmuxSession } from '../../lib/api';
import { sessionExportMarkdownUrl } from '../../lib/api';
import { shortPath } from '../../lib/format';
import { IconButton } from '../../components/IconButton';
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } from '../../components/DropdownMenu';

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
  const afterClose = useRef<(() => void) | undefined>(undefined);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <IconButton label="Session actions" icon="bi-three-dots" variant="ghost" />
      </DropdownMenuTrigger>
      <DropdownMenuContent onCloseAutoFocus={() => {
        const action = afterClose.current;
        afterClose.current = undefined;
        // Let Radix restore the trigger before the next dialog records its opener.
        if (action) queueMicrotask(action);
      }}>
        <DropdownMenuItem
          onSelect={() => { afterClose.current = onNewSession; }}
          title="New session"
        >New session</DropdownMenuItem>

        <DropdownMenuSeparator />

        <DropdownMenuItem asChild>
          <a
            href={sessionExportMarkdownUrl(sessionId)}
            download={`conversation-${sessionId}.md`}
          >Download Markdown</a>
        </DropdownMenuItem>
        <DropdownMenuItem
          onSelect={() => {
            // Defer so the menu unmounts before print snapshots the page.
            window.setTimeout(() => window.print(), 50);
          }}
        >Print / Save as PDF</DropdownMenuItem>
        <DropdownMenuItem onSelect={() => { afterClose.current = onShare; }}>Share link…</DropdownMenuItem>

        <DropdownMenuSeparator />

        {tmuxAvailable && matchingTmuxSession && (
          <DropdownMenuItem
            onClick={(e) => onTmuxSwitch(e, matchingTmuxSession.name)}
            title={`Switch tmux to ${shortPath(matchingTmuxSession.name)} (T)`}
          >Switch tmux</DropdownMenuItem>
        )}
        {tmuxAvailable && !portAvailable && liveConnectionHint && (
          <DropdownMenuItem
            onSelect={onLaunchOpencode}
            disabled={launchingOpencode}
            title="Launch opencode --port 0 in a new tmux window"
          >{launchingOpencode ? 'Launching…' : 'Launch opencode'}</DropdownMenuItem>
        )}
        <DropdownMenuItem
          onSelect={onOpenVSCode}
          title="Open in VS Code (V)"
        >Open in VS Code</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
