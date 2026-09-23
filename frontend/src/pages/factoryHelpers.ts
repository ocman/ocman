import { createContext } from 'react';
import type { FactoryIssue } from '../lib/api';

export const TRACER_FORMULA_ID = 'ocman/tracer';

export function newInstantiationID() {
  return crypto.randomUUID?.() ?? Array.from(crypto.getRandomValues(new Uint32Array(4)), (value) => value.toString(16).padStart(8, '0')).join('');
}

export const isClosed = (status: string) => status === 'closed' || status === 'completed';
export const statusLabel = (status: string) => status.replaceAll('_', ' ').replace(/^./, (letter) => letter.toUpperCase());

export type DispatchEvidence = Pick<FactoryIssue, 'dispatchState' | 'blockers' | 'retryAt' | 'retryAttempts' | 'outcomeReason'>;

// ponytail: a context instead of threading onOpen through every inbox item; rows open the drawer when a provider is present.
export const OpenIssueContext = createContext<((id: string) => void) | undefined>(undefined);
