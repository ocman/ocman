import type { ReactNode } from 'react';
import { Button } from './Control';
import styles from './InlineAlert.module.css';

interface InlineAlertProps {
  children: ReactNode;
  onRetry?: () => void;
  retrying?: boolean;
  compact?: boolean;
}

export function InlineAlert({ children, onRetry, retrying = false, compact = false }: InlineAlertProps) {
  return (
    <div className={`${styles.root}${compact ? ` ${styles.compact}` : ''}`} role="alert" data-inline-alert="">
      <span className={styles.message}>{children}</span>
      {onRetry && <Button type="button" size="small" className={styles.action} onClick={onRetry} disabled={retrying} aria-busy={retrying}>Retry</Button>}
    </div>
  );
}
