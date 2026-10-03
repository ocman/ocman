import { create } from 'zustand';

interface Submission {
  text: string;
  pending: boolean;
  error?: string;
  execute: () => Promise<void>;
}

// First command/shell/file delivery can outlive the draft composer. Keep
// its exact execution and error on the real child, independently of drafts.
export const useFirstSubmission = create<{ entries: Record<string, Submission> }>(() => ({ entries: {} }));

function clear(sessionId: string) {
  useFirstSubmission.setState(({ entries }) => {
    const next = { ...entries };
    delete next[sessionId];
    return { entries: next };
  });
}

export function startFirstSubmission(sessionId: string, text: string, execute: () => Promise<void>) {
  if (useFirstSubmission.getState().entries[sessionId]?.pending) return;
  const submission: Submission = { text, pending: true, execute };
  useFirstSubmission.setState(({ entries }) => ({ entries: { ...entries, [sessionId]: submission } }));
  void (async () => {
    try {
      await execute();
      clear(sessionId);
    } catch (error) {
      useFirstSubmission.setState(({ entries }) => ({ entries: {
        ...entries,
        [sessionId]: { ...submission, pending: false, error: error instanceof Error ? error.message : String(error) },
      } }));
    }
  })();
}
