import { EmptyState } from '../../components/EmptyState';
import { InlineAlert } from '../../components/InlineAlert';
import styles from './SessionStatusViews.module.css';

export function SessionLoadError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return <div className={styles.spacing} data-testid="error-banner"><InlineAlert onRetry={onRetry}>{message}</InlineAlert></div>;
}

export function SessionEmptyDetail({ shortcutLabel }: { shortcutLabel: string }) {
  return <EmptyState className={styles.spacing} data-testid="empty-detail">
    <p>No session open.</p>
    <p>Pick a session from the sidebar, or press <kbd>{shortcutLabel}</kbd> and run <code>/new</code> to start one.</p>
  </EmptyState>;
}
