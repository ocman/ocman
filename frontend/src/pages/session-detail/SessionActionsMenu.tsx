import { useRef } from 'react';
import { sessionExportMarkdownUrl } from '../../lib/api';
import { IconButton } from '../../components/IconButton';
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } from '../../components/DropdownMenu';

export interface SessionActionsMenuProps {
  sessionId: string;
  tmuxAvailable: boolean;
  /** Composer is live; hides the "Launch opencode" item. */
  portAvailable: boolean;
  liveConnectionHint: string | undefined;
  launchingOpencode: boolean;
  onNewSession: () => void;
  onShare: () => void;
  onLaunchOpencode: () => void;
}

/** The "⋯" session actions menu mounted into the header slot. */
export function SessionActionsMenu({
  sessionId,
  tmuxAvailable,
  portAvailable,
  liveConnectionHint,
  launchingOpencode,
  onNewSession,
  onShare,
  onLaunchOpencode,
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

        {tmuxAvailable && !portAvailable && liveConnectionHint && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onSelect={onLaunchOpencode}
              disabled={launchingOpencode}
              title="Launch opencode --port 0 in a new tmux window"
            >{launchingOpencode ? 'Launching…' : 'Launch opencode'}</DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
