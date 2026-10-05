import { createContext } from 'react';
import type { FactoryIssue } from '../lib/api';
import { randomId } from '../lib/randomId';

export const TRACER_FORMULA_ID = 'ocman/tracer';

export const newInstantiationID = randomId;

export const isClosed = (status: string) => status === 'closed' || status === 'completed';
export const statusLabel = (status: string) => status.replaceAll('_', ' ').replace(/^./, (letter) => letter.toUpperCase());

export type DispatchEvidence = Pick<FactoryIssue, 'dispatchState' | 'blockers' | 'retryAt' | 'retryAttempts' | 'outcomeReason'>;

// ponytail: a context instead of threading onOpen through every inbox item; rows open the drawer when a provider is present.
export const OpenIssueContext = createContext<((id: string) => void) | undefined>(undefined);
