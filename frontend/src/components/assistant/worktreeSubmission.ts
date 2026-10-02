import { create } from 'zustand';

interface Submission {
  text: string;
  pending: boolean;
  error?: string;
  execute: () => Promise<void>;
}

// The first request can outlive its source composer. Keep its draft, exact
// execution callback (including attachments/selections), and error on the child.
export const useWorktreeSubmission = create<{ entries: Record<string, Submission> }>(() => ({ entries: {} }));

export function clearWorktreeSubmission(sessionId: string) {
  useWorktreeSubmission.setState(({ entries }) => {
    const next = { ...entries };
    delete next[sessionId];
    return { entries: next };
  });
}

export function startWorktreeSubmission(sessionId: string, text: string, execute: () => Promise<void>) {
  const submission: Submission = { text, pending: true, execute };
  useWorktreeSubmission.setState(({ entries }) => ({ entries: { ...entries, [sessionId]: submission } }));
  void (async () => {
    try {
      await execute();
      clearWorktreeSubmission(sessionId);
    } catch (error) {
      useWorktreeSubmission.setState(({ entries }) => ({ entries: {
        ...entries,
        [sessionId]: { ...submission, pending: false, error: error instanceof Error ? error.message : String(error) },
      } }));
    }
  })();
}

// The server attempted the first send and reported a failure; keep the draft
// and a retry on the child, exactly as a failed client-side send would.
export function failWorktreeSubmission(sessionId: string, text: string, execute: () => Promise<void>, error: string) {
  useWorktreeSubmission.setState(({ entries }) => ({ entries: { ...entries, [sessionId]: { text, pending: false, error, execute } } }));
}
