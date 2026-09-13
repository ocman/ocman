import { useEffect } from 'react';
import type { Part, Session } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import type { PendingPermission } from '../../lib/sseHelpers';
import type { PendingQuestion } from '../../components/session/QuestionPrompt';
import {
  extractPendingPermission,
  extractPendingQuestion,
  extractPendingQuestionFromParts,
  hasPendingQuestionInParts,
} from '../../lib/sseHelpers';
import {
  storePendingQuestion,
  loadPendingQuestion,
  clearPendingQuestion,
} from './usePromptHandlers';

export interface UsePromptSyncOptions {
  id: string | undefined;
  session: Session | null | undefined;
  parts: Part[];
  recentSessions: Session[];
  portAvailable: boolean;
  /** The active session plus every descendant in its tree. */
  promptSessionIds: string[];
  pendingPermission: PendingPermission | null;
  pendingQuestion: PendingQuestion | null;
  clearPrompt: (kind: 'permission' | 'question', id: string) => void;
  setPendingPermission: (perm: PendingPermission, ownerIds?: string[]) => void;
  setPendingQuestion: (q: PendingQuestion) => void;
  setPermissionError: (error: string | null) => void;
}

/**
 * Keeps the reducer's pending permission/question in sync with every
 * other source of truth for "a prompt exists": the sidebar row badge,
 * the REST session payload, OpenCode's live `/question` list, and the
 * per-session sessionStorage mirror. Events are a hint; these effects
 * catch what the SSE stream missed.
 */
export function usePromptSync({
  id,
  session,
  parts,
  recentSessions,
  portAvailable,
  promptSessionIds,
  pendingPermission,
  pendingQuestion,
  clearPrompt,
  setPendingPermission,
  setPendingQuestion,
  setPermissionError,
}: UsePromptSyncOptions): void {
  const patchRecentSession = useApiStore((state) => state.patchRecentSession);
  const listPermissions = useApiStore((state) => state.listPermissions);
  const listQuestions = useApiStore((state) => state.listQuestions);

  // Mirror pending prompt flags into the sidebar row so the badge
  // lights up/clears immediately from SSE.
  useEffect(() => {
    if (!id) return;
    patchRecentSession(id, {
      pendingPermission: pendingPermission !== null,
      pendingQuestion: pendingQuestion !== null,
    });
  }, [id, pendingPermission, pendingQuestion, patchRecentSession]);

  // Reverse sync: when the session REST response or the sidebar poll
  // reports a pending prompt we don't yet have in state, fetch the
  // full detail so the dialog appears.
  //
  // Two sources signal "a prompt exists":
  //   1. session.pendingPermission / session.pendingQuestion (boolean)
  //      from the initial /api/session/{id} fetch — fires immediately on
  //      page load or session switch, without waiting for a sidebar poll.
  //   2. sidebarHasPerm / sidebarHasQuestion from the /api/sessions poll
  //      — catches prompts that arrive while the SSE stream is open but
  //      the permission.asked event was somehow missed.
  const sidebarCurrentSession = recentSessions.find((s) => s.id === id);
  const sidebarHasPerm = sidebarCurrentSession?.pendingPermission ?? false;
  const sidebarHasQuestion = sidebarCurrentSession?.pendingQuestion ?? false;
  const restHasPerm = session?.pendingPermission ?? false;
  const restHasQuestion = session?.pendingQuestion ?? false;
  useEffect(() => {
    if (!id) return;
    // These fetches outlive a navigation. Without the flag, a response
    // that lands after the user moved from session A to B injects A's
    // prompt into B and disables B's composer behind a phantom dialog.
    let cancelled = false;
    if ((restHasPerm || sidebarHasPerm) && pendingPermission === null) {
      Promise.all(promptSessionIds.map((sessionId) => listPermissions(sessionId)))
        .then((permissionLists) => {
          if (cancelled) return;
          for (const raw of permissionLists.flat()) {
            const p = raw as Record<string, unknown>;
            const perm = extractPendingPermission({ type: 'permission.asked', properties: p });
            if (!perm) continue;
            setPendingPermission(perm, promptSessionIds);
            setPermissionError(null);
            break;
          }
        })
        .catch(() => { /* sidebar will retry */ });
    }
    if ((restHasQuestion || sidebarHasQuestion) && pendingQuestion === null) {
      listQuestions(id)
        .then((questions) => {
          if (cancelled) return;
          for (const raw of questions) {
            const q = raw as Record<string, unknown>;
            const question = extractPendingQuestion({ type: 'question.asked', properties: q });
            if (!question) continue;
            storePendingQuestion(id, question);
            setPendingQuestion(question);
            break;
          }
        })
        .catch(() => { /* sidebar will retry */ });
    }
    return () => { cancelled = true; };
  }, [id, restHasPerm, restHasQuestion, sidebarHasPerm, sidebarHasQuestion,
    pendingPermission, pendingQuestion,
    listPermissions, listQuestions, promptSessionIds, setPermissionError, setPendingPermission,
    setPendingQuestion]);

  // Poll-driven dismissal fallback. When a question is answered
  // outside ocman (e.g. directly in the OpenCode CLI), OpenCode does
  // not reliably emit a `question.replied` SSE event, so the reducer
  // never clears the prompt and it stays on screen indefinitely.
  //
  // The sidebar's pendingQuestion flag is too coarse to drive this: it
  // says "some prompt exists for this row" (subagent prompts bubble up)
  // and lags a 3 s poll. Instead, poll OpenCode's authoritative live
  // `/question` list directly: when the currently-pending requestId is
  // no longer in it, the question has been answered/cancelled somewhere
  // and the prompt must come down. Matching on the requestId (rather
  // than an empty list) avoids dismissing a freshly-asked follow-up.
  const pendingQuestionRequestId = pendingQuestion?.requestId ?? null;
  useEffect(() => {
    if (!id || !pendingQuestionRequestId || !portAvailable) return;
    let cancelled = false;

    const check = () => {
      listQuestions(id)
        .then((questions) => {
          if (cancelled) return;
          const stillPending = questions.some((raw) => {
            const q = extractPendingQuestion({ type: 'question.asked', properties: raw as Record<string, unknown> });
            return q?.requestId === pendingQuestionRequestId;
          });
          if (stillPending) return;
          // Both clears are id-safe: they only fire if the request id
          // still matches, so a follow-up asked between the poll and its
          // response survives — in memory *and* in session storage.
          clearPrompt('question', pendingQuestionRequestId);
          clearPendingQuestion(id, pendingQuestionRequestId);
        })
        .catch(() => { /* leave the prompt up; the next tick retries */ });
    };

    const timer = window.setInterval(() => {
      if (!document.hidden) check();
    }, 3000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [id, pendingQuestionRequestId, portAvailable, listQuestions, clearPrompt]);

  // Restore pending question from sessionStorage when navigating
  // back to a page whose parts still show a pending question tool.
  useEffect(() => {
    if (pendingQuestion || !session?.id || !portAvailable) return;
    const pendingFromParts = extractPendingQuestionFromParts(parts, session.id);
    if (pendingFromParts) {
      storePendingQuestion(session.id, pendingFromParts);
      setPendingQuestion(pendingFromParts);
      return;
    }
    if (!hasPendingQuestionInParts(parts, session.id)) return;
    const stored = loadPendingQuestion(session.id);
    if (stored) setPendingQuestion(stored);
  }, [parts, session?.id, portAvailable, pendingQuestion, setPendingQuestion]);
}
