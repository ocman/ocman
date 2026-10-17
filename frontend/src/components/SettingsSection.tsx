import type { ReactNode } from 'react';
import styles from '../pages/dashboard/SettingsTab.module.css';

/** A visible subdivision of a settings category, never another navigation level. */
export function SettingsSection({ title, children }: { title: string; children: ReactNode }) {
  return <section className={styles.section} aria-label={title}>
    <h3 className={styles.sectionTitle}>{title}</h3>
    {children}
  </section>;
}
