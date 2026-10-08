import type { ComponentProps } from 'react';
import styles from './FilterField.module.css';

export function FilterField({ label, compact = false, className = '', children, ...props }: ComponentProps<'div'> & { label?: string; compact?: boolean }) {
  return <div {...props} className={`${styles.field} ${compact ? styles.compact : ''} ${className}`}>
    {label && <span className={styles.caption}>{label}</span>}
    {children}
  </div>;
}
