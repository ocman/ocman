import { memo } from 'react';
import * as Toast from '@radix-ui/react-toast';
import type { LaunchStatus } from '../../lib/createSessionWithLaunch';

export interface SessionToastsProps {
  showRenameToast: boolean;
  setShowRenameToast: (v: boolean) => void;
  createLaunchStatus: LaunchStatus;
  showCreateSessionErrorToast: boolean;
  setShowCreateSessionErrorToast: (v: boolean) => void;
  showDisconnectedToast: boolean;
  setShowDisconnectedToast: (v: boolean) => void;
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
  createLaunchStatus,
  showCreateSessionErrorToast,
  setShowCreateSessionErrorToast,
  showDisconnectedToast,
  setShowDisconnectedToast,
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
        className="oc-toast-root"
        open={createLaunchStatus !== 'idle'}
        duration={Infinity}
      >
        <Toast.Description className="oc-toast-description">
          {createLaunchStatus === 'launching'
            ? 'Launching opencode in tmux…'
            : 'Waiting for opencode to start…'}
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
