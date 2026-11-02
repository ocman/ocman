import type { ReactNode } from 'react';
import { Button } from './Control';
import styles from './PageState.module.css';

/**
 * Dedicated empty/unavailable content; refresh failures use InlineAlert instead.
 * An error state defaults to a warning icon and offers Retry when onRetry is set;
 * `compact` fits it into a pane instead of a page.
 */
export function PageState({ title, description, icon, error = false, compact = false, onRetry, retrying = false, children }: {
  title: string;
  description?: ReactNode;
  icon?: string;
  error?: boolean;
  compact?: boolean;
  onRetry?: () => void;
  retrying?: boolean;
  children?: ReactNode;
}) {
  const iconClass = icon ?? (error ? 'bi-exclamation-circle' : undefined);
  const className = [styles.root, error && styles.error, compact && styles.compact].filter(Boolean).join(' ');
  return <section className={className} aria-label={title} role={error ? 'alert' : undefined} data-error-state={error ? '' : undefined}>
    {iconClass && <i className={`bi ${iconClass} ${styles.icon}`} aria-hidden="true" />}
    <h2 className={styles.heading}>{title}</h2>
    {description && <p className={styles.description}>{description}</p>}
    {onRetry && <Button type="button" size={compact ? 'small' : 'normal'} onClick={onRetry} disabled={retrying} aria-busy={retrying}>Retry</Button>}
    {children}
  </section>;
}
