import { createContext, useContext } from 'react';
import type { FailedSend } from './failedSends';

export interface FailedSendsContextValue {
  /**
   * Lookup of failed-send entries by their optimistic message id. Used by
   * the assistant thread's user-message renderer to surface a Retry banner
   * on the matching bubble.
   */
  byId: Record<string, FailedSend>;
  /** Replay the failed send with the given message id. */
  retry: (id: string) => void;
  /** Drop the failed send (removes both the persisted entry and the bubble). */
  dismiss: (id: string) => void;
}

// Default no-op so consumers outside a session page (e.g. story / test
// fixtures) don't need to wrap a provider just to render the thread.
const DEFAULT: FailedSendsContextValue = {
  byId: {},
  retry: () => {},
  dismiss: () => {},
};

export const FailedSendsContext = createContext<FailedSendsContextValue>(DEFAULT);

export function useFailedSends(): FailedSendsContextValue {
  return useContext(FailedSendsContext);
}
