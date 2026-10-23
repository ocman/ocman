import { useEffect, useRef } from 'react';
import type { Part, Session } from '../../lib/api';
import { api } from '../../lib/api';
import type { PendingPermission } from '../../lib/sseHelpers';
import type { PendingQuestion } from '../../components/session/QuestionPrompt';
import {
  extractPendingQuestionFromParts,
  hasPendingQuestionInParts,
  permissionFromRequest,
  questionFromRequest,
} from '../../lib/sseHelpers';
import {
  storePendingQuestion,
  loadPendingQuestion,
  clearPendingQuestion,
} from './usePromptHandlers';
import { patchSessionLists } from '../../lib/sessionsCache';

export interface UsePromptSyncOptions {
  id: string | undefined;
  session: Session | null | undefined;
  parts: Part[];
  recentSessions: Session[];
  portAvailable: boolean;
  /**
   * The owner's stream delivers every reply (AD-7a), so no dismissal poll
   * runs: the stream's replied/rejected events clear the prompt.
   */
  pushPrompts?: boolean;
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
  pushPrompts = false,
  promptSessionIds,
  pendingPermission,
  pendingQuestion,
  clearPrompt,
  setPendingPermission,
  setPendingQuestion,
  setPermissionError,
}: UsePromptSyncOptions): void {
  const { listPermissions, refreshPermissions, listQuestions } = api;

  // Mirror pending prompt flags into the sidebar row so the badge
  // lights up/clears immediately from SSE.
  useEffect(() => {
    if (!id) return;
    patchSessionLists(id, {
      pendingPermission: pendingPermission !== null,
      pendingQuestion: pendingQuestion !== null,
    }, session?.platform);
  }, [id, pendingPermission, pendingQuestion, session?.platform]);

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
  const ownerPlatform = session?.platform;
  const sidebarCurrentSession = recentSessions.find((s) => s.id === id && (!ownerPlatform || s.platform === ownerPlatform));
  const sidebarHasPerm = sidebarCurrentSession?.pendingPermission ?? false;
  const sidebarHasQuestion = sidebarCurrentSession?.pendingQuestion ?? false;
  const restHasPerm = session?.pendingPermission ?? false;
  const restHasQuestion = session?.pendingQuestion ?? false;
  // Mobile leaves the sidebar unloaded. Recover prompts directly once this
  // conversation is live, even when historical REST rows carry no prompt flags.
  const liveKey = id && session?.id === id && session.platform && portAvailable
    ? JSON.stringify([session.platform, id]) : null;
  const permissionKey = liveKey ? JSON.stringify([liveKey, promptSessionIds]) : null;
  const recoveredPermissions = useRef<string | null>(null);
  const recoveredQuestions = useRef<string | null>(null);
  useEffect(() => {
    if (!id || session?.id !== id || !ownerPlatform) return;
    const discover = permissionKey !== null && recoveredPermissions.current !== permissionKey;
    if (!permissionKey || pendingPermission !== null) recoveredPermissions.current = permissionKey;
    // These fetches outlive a navigation. Without the flag, a response
    // that lands after the user moved from session A to B injects A's
    // prompt into B and disables B's composer behind a phantom dialog.
    let cancelled = false;
    if ((discover || restHasPerm || sidebarHasPerm) && pendingPermission === null) {
      Promise.all(promptSessionIds.map((sessionId) => listPermissions(sessionId, ownerPlatform)))
        .then((permissionLists) => {
          if (cancelled) return;
          recoveredPermissions.current = permissionKey;
          for (const request of permissionLists.flat()) {
            const perm = permissionFromRequest(request);
            if (!perm) continue;
            setPendingPermission(perm, promptSessionIds);
            setPermissionError(null);
            break;
          }
        })
        .catch(() => { /* a later connection or prompt flag change retries */ });
    }
    return () => { cancelled = true; };
  }, [id, session?.id, ownerPlatform, permissionKey, restHasPerm, sidebarHasPerm, pendingPermission,
    listPermissions, promptSessionIds, setPermissionError, setPendingPermission]);

  // Independent lifetimes keep a permission update from cancelling question
  // recovery, and vice versa. Successful reads do not repeat after a reply.
  useEffect(() => {
    if (!id || session?.id !== id || !ownerPlatform) return;
    const discover = liveKey !== null && recoveredQuestions.current !== liveKey;
    if (!liveKey || pendingQuestion !== null) recoveredQuestions.current = liveKey;
    let cancelled = false;
    if ((discover || restHasQuestion || sidebarHasQuestion) && pendingQuestion === null) {
      listQuestions(id, ownerPlatform)
        .then((questions) => {
          if (cancelled) return;
          recoveredQuestions.current = liveKey;
          for (const request of questions) {
            const question = questionFromRequest(request);
            if (!question) continue;
            storePendingQuestion(id, question);
            setPendingQuestion(question);
            break;
          }
        })
        .catch(() => { /* a later connection or prompt flag change retries */ });
    }
    return () => { cancelled = true; };
  }, [id, session?.id, ownerPlatform, liveKey, restHasQuestion, sidebarHasQuestion, pendingQuestion,
    listQuestions, setPendingQuestion]);

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
    if (!id || !ownerPlatform || !pendingQuestionRequestId || !portAvailable || pushPrompts) return;
    let cancelled = false;

    const check = () => {
      listQuestions(id, ownerPlatform)
        .then((questions) => {
          if (cancelled) return;
          const stillPending = questions.some((request) => questionFromRequest(request)?.requestId === pendingQuestionRequestId);
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
  }, [id, ownerPlatform, pendingQuestionRequestId, portAvailable, pushPrompts, listQuestions, clearPrompt]);

  // Same fallback for permissions. A reply that lands while the stream
  // is down (sleep, judge auto-approval, another tab) is never replayed,
  // and a reconnect's reconcile keeps the in-memory prompt. Only the
  // authoritative refresh may prove absence: ocman's observed-prompt cache
  // can miss a prompt the session stream delivered. A failed refresh keeps
  // the prompt, and a miss must repeat on the next poll before dismissal.
  const pendingPermissionId = pendingPermission?.permissionId ?? null;
  const pendingPermissionSessionId = pendingPermission?.sessionId || id;
  // Descendants run on the viewed session's owner.
  useEffect(() => {
    if (!pendingPermissionSessionId || !pendingPermissionId || !ownerPlatform || !portAvailable || pushPrompts) return;
    let cancelled = false;
    let misses = 0;
    const timer = window.setInterval(() => {
      if (document.hidden) return;
      refreshPermissions(pendingPermissionSessionId, ownerPlatform)
        .then((permissions) => {
          if (cancelled) return;
          const stillPending = permissions.some((request) => permissionFromRequest(request)?.permissionId === pendingPermissionId);
          misses = stillPending ? 0 : misses + 1;
          if (misses >= 2) clearPrompt('permission', pendingPermissionId);
        })
        .catch(() => { /* leave the prompt up; the next tick retries */ });
    }, 3000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [pendingPermissionSessionId, pendingPermissionId, ownerPlatform, portAvailable, pushPrompts, refreshPermissions, clearPrompt]);

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
