import { memo } from 'react';
import * as Toast from '@radix-ui/react-toast';

export interface SessionToastsProps {
  showRenameToast: boolean;
  setShowRenameToast: (v: boolean) => void;
  restartToastMessage: string | null;
  setRestartToastMessage: (v: string | null) => void;
  showCreateSessionErrorToast: boolean;
  setShowCreateSessionErrorToast: (v: boolean) => void;
  showDisconnectedToast: boolean;
  setShowDisconnectedToast: (v: boolean) => void;
  copyToastMessage: string | null;
  setCopyToastMessage: (v: string | null) => void;
  sendRetryDelaySeconds: number | null;
  setSendRetryDelaySeconds: (v: number | null) => void;
  tmuxAvailable: boolean;
  liveConnectionHint: boolean;
  hasDirectory: boolean;
  launchingOpencode: boolean;
  onLaunch: () => void;
}

/**
 * Memoized Toast subtree. Radix Toast's `composeRefs` inside
 * `Toast.Viewport` triggers a `setState` during the React commit
 * phase, which can cascade into "Maximum update depth exceeded" when
 * the parent re-renders rapidly (SSE deltas, polling). Wrapping the
 * Toast tree in `memo` ensures it only re-renders when its own props
 * change, not on every SessionDetail state update.
 */
export const SessionToasts = memo(function SessionToasts({
  showRenameToast,
  setShowRenameToast,
  restartToastMessage,
  setRestartToastMessage,
  showCreateSessionErrorToast,
  setShowCreateSessionErrorToast,
  showDisconnectedToast,
  setShowDisconnectedToast,
  copyToastMessage,
  setCopyToastMessage,
  sendRetryDelaySeconds,
  setSendRetryDelaySeconds,
  tmuxAvailable,
  liveConnectionHint,
  hasDirectory,
  launchingOpencode,
  onLaunch,
}: SessionToastsProps) {
  return (
    <>
      <Toast.Root className="oc-toast-root" open={showRenameToast} onOpenChange={setShowRenameToast} duration={2000}>
        <Toast.Description className="oc-toast-description">
          Session renamed
        </Toast.Description>
      </Toast.Root>
      <Toast.Root
        key={restartToastMessage ?? 'restart-hidden'}
        className="oc-toast-root"
        open={restartToastMessage !== null}
        onOpenChange={(open) => { if (!open) setRestartToastMessage(null); }}
        duration={restartToastMessage === 'Restarted OpenCode' ? 3000 : 60000}
      >
        <Toast.Description className="oc-toast-description">
          {restartToastMessage}
        </Toast.Description>
      </Toast.Root>
      <Toast.Root
        key={copyToastMessage ?? 'copy-hidden'}
        className="oc-toast-root"
        open={copyToastMessage !== null}
        onOpenChange={(open) => { if (!open) setCopyToastMessage(null); }}
        duration={2500}
      >
        <Toast.Description className="oc-toast-description">
          {copyToastMessage}
        </Toast.Description>
      </Toast.Root>
      <Toast.Root
        key={sendRetryDelaySeconds ?? 'send-retry-hidden'}
        className="oc-toast-root error"
        open={sendRetryDelaySeconds !== null}
        onOpenChange={(open) => { if (!open) setSendRetryDelaySeconds(null); }}
      >
        <Toast.Description className="oc-toast-description">
          Backend is not responding. Retrying in {sendRetryDelaySeconds}s…
        </Toast.Description>
      </Toast.Root>
      <Toast.Root
        className="oc-toast-root error"
        open={showCreateSessionErrorToast}
        onOpenChange={setShowCreateSessionErrorToast}
        duration={3500}
      >
        <Toast.Description className="oc-toast-description">
          Failed to create session
        </Toast.Description>
      </Toast.Root>
      <Toast.Root
        className="oc-toast-root error"
        open={showDisconnectedToast}
        onOpenChange={setShowDisconnectedToast}
        duration={8000}
      >
        <Toast.Description className="oc-toast-description">
          <div className="oc-toast-body">
            <span>OpenCode is not running for this session.</span>
            {tmuxAvailable && liveConnectionHint && hasDirectory && (
              <button
                type="button"
                className="oc-toast-action"
                disabled={launchingOpencode}
                onClick={() => {
                  setShowDisconnectedToast(false);
                  onLaunch();
                }}
              >{launchingOpencode ? 'Launching…' : 'Launch opencode'}</button>
            )}
          </div>
        </Toast.Description>
      </Toast.Root>
      <Toast.Viewport className="oc-toast-viewport" />
    </>
  );
});
