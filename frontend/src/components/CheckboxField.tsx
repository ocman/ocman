import type { ComponentPropsWithRef, ReactNode } from 'react';
import styles from './CheckboxField.module.css';

export function CheckboxField({ label, ...props }: Omit<ComponentPropsWithRef<'input'>, 'type'> & { label: ReactNode }) {
  return <label className={styles.label}><input {...props} type="checkbox" className={`${styles.input} ${props.className ?? ''}`} /><span>{label}</span></label>;
}
