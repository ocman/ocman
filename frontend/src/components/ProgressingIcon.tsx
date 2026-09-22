import type { HTMLAttributes } from 'react';
import './ProgressingIcon.css';

export function ProgressingIcon({ className, ...props }: HTMLAttributes<HTMLSpanElement>) {
  const labelled = props['aria-label'] || props['aria-labelledby'];
  return (
    <span
      aria-hidden={labelled ? undefined : true}
      {...props}
      className={`oc-progressing-icon${className ? ` ${className}` : ''}`}
    />
  );
}
