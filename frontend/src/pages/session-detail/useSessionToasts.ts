import { useState } from 'react';

/**
 * The page's transient toast state, grouped so the page can spread it
 * into `<SessionToasts>` and the setter half into `useSessionActions`.
 */
export function useSessionToasts() {
  const [showRenameToast, setShowRenameToast] = useState(false);
  const [restartToastMessage, setRestartToastMessage] = useState<string | null>(null);
  const [showCreateSessionErrorToast, setShowCreateSessionErrorToast] = useState(false);
  const [showDisconnectedToast, setShowDisconnectedToast] = useState(false);
  const [copyToastMessage, setCopyToastMessage] = useState<string | null>(null);
  const [sendRetryDelaySeconds, setSendRetryDelaySeconds] = useState<number | null>(null);
  return {
    state: {
      showRenameToast,
      restartToastMessage,
      showCreateSessionErrorToast,
      showDisconnectedToast,
      copyToastMessage,
      sendRetryDelaySeconds,
    },
    setters: {
      setShowRenameToast,
      setRestartToastMessage,
      setShowCreateSessionErrorToast,
      setShowDisconnectedToast,
      setCopyToastMessage,
      setSendRetryDelaySeconds,
    },
  };
}
