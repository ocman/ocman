import type { ReactNode } from 'react';
import { Button } from './Control';

interface InlineAlertProps {
  children: ReactNode;
  onRetry?: () => void;
  retrying?: boolean;
}

export function InlineAlert({ children, onRetry, retrying = false }: InlineAlertProps) {
  return (
    <div className="oc-error-banner" role="alert">
      <span>{children}</span>
      {onRetry && <Button type="button" size="small" onClick={onRetry} disabled={retrying} aria-busy={retrying}>Retry</Button>}
    </div>
  );
}
