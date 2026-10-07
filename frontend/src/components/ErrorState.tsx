import type { ReactNode } from 'react';
import styles from './ErrorState.module.css';

export function ErrorState({ title, description, inline = false, children }: { title: string; description?: ReactNode; inline?: boolean; children?: ReactNode }) {
  return <div className={`${styles.root}${inline ? ` ${styles.inline}` : ''}`} role="alert" data-error-state="">
    <h2 className={styles.heading}>{title}</h2>
    {description && <p className={styles.description}>{description}</p>}
    {children}
  </div>;
}
