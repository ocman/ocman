import type { ComponentPropsWithRef } from 'react';
import styles from './ToggleField.module.css';

export function ToggleField({ label, className = '', ...props }: Omit<ComponentPropsWithRef<'input'>, 'type' | 'aria-label'> & { label: string }) {
  return <label className={styles.toggle}>
    <input {...props} type="checkbox" aria-label={label} className={`${styles.input} ${className}`} />
    <span className={styles.track} aria-hidden="true" />
  </label>;
}
