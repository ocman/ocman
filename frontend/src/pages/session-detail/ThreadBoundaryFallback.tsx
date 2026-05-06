import { useEffect, useRef } from 'react';

export function ThreadBoundaryFallback({
  error,
  reset,
  autoRecover,
  onReload,
}: {
  error: Error;
  reset: () => void;
  autoRecover: boolean;
  onReload: () => void;
}) {
  const autoTriggeredRef = useRef(false);

  useEffect(() => {
    if (!autoRecover || autoTriggeredRef.current) return;
    autoTriggeredRef.current = true;
    onReload();
  }, [autoRecover, onReload]);

  if (autoRecover) {
    return (
      <div className="oc-error-boundary" role="alert">
        <h2>Recovering session thread…</h2>
        <p>{error.message}</p>
      </div>
    );
  }

  return (
    <div className="oc-error-boundary" role="alert">
      <h2>Something went wrong</h2>
      <p>{error.message || 'An unexpected error occurred while rendering this view.'}</p>
      <div style={{ display: 'flex', gap: 8, justifyContent: 'center', flexWrap: 'wrap' }}>
        <button type="button" onClick={onReload}>Reload thread</button>
        <button type="button" onClick={reset}>Try again</button>
      </div>
    </div>
  );
}
