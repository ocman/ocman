import { useEffect, useRef } from 'react';
import { Button, ButtonGroup } from '../../components/Control';
import { ErrorState } from '../../components/ErrorState';

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
      <ErrorState title="Recovering session thread…" description={error.message} />
    );
  }

  return (
    <ErrorState title="Something went wrong" description={error.message || 'An unexpected error occurred while rendering this view.'}>
      <ButtonGroup label="Thread recovery">
        <Button type="button" onClick={onReload}>Reload thread</Button>
        <Button type="button" onClick={reset}>Try again</Button>
      </ButtonGroup>
    </ErrorState>
  );
}
