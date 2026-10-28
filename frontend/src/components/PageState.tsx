import type { ReactNode } from 'react';
import styles from './PageState.module.css';

/** Dedicated empty/unavailable content; refresh failures use InlineAlert instead. */
export function PageState({ title, description, icon, error = false, children }: {
  title: string;
  description: ReactNode;
  icon: string;
  error?: boolean;
  children?: ReactNode;
}) {
  return <section className={`${styles.root}${error ? ` ${styles.error}` : ''}`} aria-label={title} role={error ? 'alert' : undefined}>
    <i className={`bi ${icon} ${styles.icon}`} aria-hidden="true" />
    <h2 className={styles.heading}>{title}</h2>
    <p className={styles.description}>{description}</p>
    {children}
  </section>;
}
