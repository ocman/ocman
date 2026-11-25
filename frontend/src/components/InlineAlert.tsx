import type { ReactNode } from 'react';
import { Button } from './Control';
import styles from './InlineAlert.module.css';

interface InlineAlertProps {
  children: ReactNode;
  onRetry?: () => void;
  retryLabel?: string;
  retrying?: boolean;
  compact?: boolean;
  tone?: 'error' | 'warning';
}

export function InlineAlert({ children, onRetry, retryLabel = 'Retry', retrying = false, compact = false, tone = 'error' }: InlineAlertProps) {
  return (
    <div className={`${styles.root}${compact ? ` ${styles.compact}` : ''}`} role={tone === 'warning' ? 'status' : 'alert'} data-inline-alert="" data-tone={tone}>
      <span className={styles.message}>{children}</span>
      {onRetry && <Button type="button" size="small" className={styles.action} onClick={onRetry} disabled={retrying} aria-busy={retrying}>{retryLabel}</Button>}
    </div>
  );
}
