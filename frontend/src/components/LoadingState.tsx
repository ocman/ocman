import type { ReactNode } from 'react';
import { Spinner } from './Spinner';
import './LoadingState.css';

export function LoadingState({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <div className={`oc-loading-state ${className}`} role="status"><Spinner />{children}</div>;
}
