import type { ReactNode } from 'react';
import { Button } from './Control';

interface InlineAlertProps {
  children: ReactNode;
  onRetry?: () => void;
  retrying?: boolean;
  compact?: boolean;
}

export function InlineAlert({ children, onRetry, retrying = false, compact = false }: InlineAlertProps) {
  return (
    <div className={`oc-error-banner${compact ? ' oc-error-banner--compact' : ''}`} role="alert">
      <span>{children}</span>
      {onRetry && <Button type="button" size="small" onClick={onRetry} disabled={retrying} aria-busy={retrying}>Retry</Button>}
    </div>
  );
}
