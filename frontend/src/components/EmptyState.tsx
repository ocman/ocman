import type { HTMLAttributes } from 'react';

export function EmptyState({ className = '', ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div {...props} className={`oc-empty ${className}`} />;
}
