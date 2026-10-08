import type { ReactNode } from 'react';
import styles from './RoutineStateBadge.module.css';

export function RoutineStateBadge({ state, children = state }: { state: string; children?: ReactNode }) {
  return <span className={styles.badge} data-state={state}>{children}</span>;
}
