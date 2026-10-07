import { create } from 'zustand';

interface Submission {
  text: string;
  pending: boolean;
  error?: string;
  execute?: () => Promise<void>;
}

const PREFIX = 'ocman.firstSubmission.v1:';
function read(sessionId: string): Submission | undefined {
  try {
    const entry = JSON.parse(localStorage.getItem(PREFIX + sessionId) || 'null');
    if (entry && typeof entry.text === 'string' && typeof entry.pending === 'boolean' &&
      (entry.error === undefined || typeof entry.error === 'string')) return entry;
  } catch { /* Keep this tab's live delivery state if browser storage is unavailable. */ }
}

function load() {
  const entries: Record<string, Submission> = {};
  try {
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);
      if (!key?.startsWith(PREFIX)) continue;
      const id = key.slice(PREFIX.length);
      const entry = read(id);
      if (entry) entries[id] = entry;
    }
  } catch { /* Browser-local state remains usable. */ }
  return entries;
}

// First command/shell/file delivery can outlive the draft composer. Keep
// its exact execution and error on the real child, independently of drafts.
export const useFirstSubmission = create<{ entries: Record<string, Submission> }>(() => ({ entries: load() }));
export const getFirstSubmission = (id: string) => read(id) || useFirstSubmission.getState().entries[id];

function publish(sessionId: string, submission: Submission) {
  try { localStorage.setItem(PREFIX + sessionId, JSON.stringify(submission)); } catch { /* Keep the originating tab's live retry. */ }
  useFirstSubmission.setState(({ entries }) => ({ entries: { ...entries, [sessionId]: submission } }));
}

function clear(sessionId: string) {
  try { localStorage.removeItem(PREFIX + sessionId); } catch { /* Retain the live completion. */ }
  useFirstSubmission.setState(({ entries }) => {
    const next = { ...entries };
    delete next[sessionId];
    return { entries: next };
  });
}

export function startFirstSubmission(sessionId: string, text: string, execute: () => Promise<void>) {
  if (useFirstSubmission.getState().entries[sessionId]?.pending) return;
  const submission: Submission = { text, pending: true, execute };
  publish(sessionId, submission);
  void (async () => {
    try {
      await execute();
      clear(sessionId);
    } catch (error) {
      publish(sessionId, { ...submission, pending: false, error: error instanceof Error ? error.message : String(error) });
    }
  })();
}

if (typeof window !== 'undefined') window.addEventListener('storage', (event) => {
  if (!event.key?.startsWith(PREFIX)) return;
  const id = event.key.slice(PREFIX.length);
  const entry = read(id);
  useFirstSubmission.setState(({ entries }) => {
    const next = { ...entries };
    if (entry) next[id] = { ...entry, execute: entries[id]?.execute };
    else delete next[id];
    return { entries: next };
  });
});
