import type { StartSessionResponse } from './api.types';
import { transact } from './draftDb';

export interface DraftStart {
  version: number;
  text: string;
  sessionId?: string;
  error?: string;
  routeKey?: string;
  createdSession?: Pick<StartSessionResponse, 'sessionId' | 'platform' | 'remoteId' | 'directory'>;
  attemptId?: string;
  replacementDraftId?: string;
  /** The terminal outcome is known in this tab but could not be stored yet. */
  persistenceError?: string;
  /** The session exists; the draft could not be retired/relocated yet. */
  relocationError?: string;
  /** What the initiating composer submitted: the retirement ownership check. */
  submitted?: { revision: number; routeKey: string; selections: string };
  deliveryState?: 'pending' | 'failed' | 'interrupted' | 'done';
  deliveryOwner?: string;
}

// Completed, released and session-created records are final, even when they kept an old error.
const isFinal = (start: DraftStart) => !start.error || !!start.sessionId || start.deliveryState === 'done';

export async function claimDraftStart(key: string, next: DraftStart) {
  return transact(['starts'], 'readwrite', async (tx) => {
    const current = await tx.get<DraftStart>('starts', key);
    if (current && isFinal(current)) return { claimed: false, start: current };
    tx.put('starts', key, next);
    return { claimed: true, start: next };
  });
}

/** Write a terminal outcome for its own attempt; an older attempt or a released record wins. */
export const persistDraftStart = (key: string, start: DraftStart) => transact(['starts'], 'readwrite', async (tx) => {
  const current = await tx.get<DraftStart>('starts', key);
  if (current && (current.attemptId !== start.attemptId || current.deliveryState === 'done')) return current;
  tx.put('starts', key, start);
  return start;
});

export const readDraftStart = (key: string) => transact(['starts'], 'readonly', (tx) => tx.get<DraftStart>('starts', key));
