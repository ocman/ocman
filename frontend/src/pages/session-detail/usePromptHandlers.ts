import { useCallback, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import { useApiStore } from '../../lib/apiStore';
import type { PlatformCapabilities } from '../../lib/api';
import type { PendingPermission } from '../../lib/sseHelpers';
import type { PendingQuestion } from '../../components/session/QuestionPrompt';
import { notifyPromptDismissed } from '../../lib/useToastNotify';

/**
 * sessionStorage key prefix used to persist a pending question
 * across reloads. The state is per-session so two browser tabs
 * looking at different sessions don't clobber each other.
 */
const PENDING_QUESTION_KEY = 'ocman:pendingQuestion:';

/** Persist the active question so a refresh re-mounts it. */
export function storePendingQuestion(sessionId: string, question: PendingQuestion) {
  try {
    sessionStorage.setItem(PENDING_QUESTION_KEY + sessionId, JSON.stringify(question));
  } catch {
    /* quota exceeded or unavailable */
  }
}

/** Read a previously persisted question for the session, if any. */
export function loadPendingQuestion(sessionId: string): PendingQuestion | null {
  try {
    const raw = sessionStorage.getItem(PENDING_QUESTION_KEY + sessionId);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (parsed && parsed.requestId && Array.isArray(parsed.questions) && parsed.questions.length > 0) {
      return parsed as PendingQuestion;
    }
  } catch {
    /* corrupt or unavailable */
  }
  return null;
}

/** Drop the persisted question for the session. */
export function clearPendingQuestion(sessionId: string) {
  try {
    sessionStorage.removeItem(PENDING_QUESTION_KEY + sessionId);
  } catch {
    /* unavailable */
  }
}

export interface UsePromptHandlersOptions {
  session: { id: string } | null;
  portAvailable: boolean;
  caps: PlatformCapabilities;
  pendingPermission: PendingPermission | null;
  setPendingPermission: Dispatch<SetStateAction<PendingPermission | null>>;
  pendingQuestion: PendingQuestion | null;
  setPendingQuestion: Dispatch<SetStateAction<PendingQuestion | null>>;
}

export interface UsePromptHandlersResult {
  /** True while a permission reply POST is in flight. */
  answeringPermission: boolean;
  /** Surfaced under the prompt when respondPermission rejects. */
  permissionError: string | null;
  setPermissionError: Dispatch<SetStateAction<string | null>>;
  /** True while a question reply / reject POST is in flight. */
  answeringQuestion: boolean;
  /** Surfaced under the prompt when respondQuestion rejects. */
  questionError: string | null;
  setQuestionError: Dispatch<SetStateAction<string | null>>;
  /** Submit a reply to the pending permission. Idempotent against
   *  rapid clicks (gated by `answeringPermission`). */
  handlePermissionReply: (reply: 'once' | 'always' | 'reject') => Promise<void>;
  /** Submit answers to the pending question. */
  handleQuestionReply: (answers: string[][]) => Promise<void>;
  /** Reject the pending question without answering. */
  handleQuestionReject: () => Promise<void>;
}

/**
 * Owns the response state for the per-session permission and
 * question prompts: in-flight flag, error message, and the three
 * handlers (allow / reply / reject) that POST to the platform API
 * and clear the prompt on success.
 *
 * The pending state itself stays in the page so the SSE handler
 * can also write to it; this hook only manages the post-back side.
 */
export function usePromptHandlers({
  session,
  portAvailable,
  caps,
  pendingPermission,
  setPendingPermission,
  pendingQuestion,
  setPendingQuestion,
}: UsePromptHandlersOptions): UsePromptHandlersResult {
  const respondPermission = useApiStore((s) => s.respondPermission);
  const respondQuestion = useApiStore((s) => s.respondQuestion);
  const rejectQuestion = useApiStore((s) => s.rejectQuestion);

  const [answeringPermission, setAnsweringPermission] = useState(false);
  const [permissionError, setPermissionError] = useState<string | null>(null);
  const [answeringQuestion, setAnsweringQuestion] = useState(false);
  const [questionError, setQuestionError] = useState<string | null>(null);

  const handlePermissionReply = useCallback(async (reply: 'once' | 'always' | 'reject') => {
    if (!pendingPermission || answeringPermission || !portAvailable || !caps.respondPermission || !session) return;
    setPermissionError(null);
    setAnsweringPermission(true);
    const repliedId = pendingPermission.permissionId;
    // OpenCode's permission API is session-scoped: the URL is
    // /session/{id}/permissions/{pid}. When the prompt comes from a
    // subagent of the page session, route the reply to that
    // subagent's session — the parent session knows nothing about
    // the prompt.
    const targetSessionId = pendingPermission.sessionId || session.id;
    try {
      await respondPermission(targetSessionId, repliedId, reply);
      // Only clear the prompt if the currently pending permission
      // is still the one we just replied to. An SSE
      // `permission.asked` event for a follow-up may have already
      // arrived while the POST was in flight — clearing
      // unconditionally would hide that new prompt.
      setPendingPermission((prev) => (prev && prev.permissionId === repliedId ? null : prev));
      // Drop any global prompt toast pointing at this session — the
      // user just answered. Cross-tab clients still get pruned on
      // their next poll.
      notifyPromptDismissed(targetSessionId);
      // SSE events will deliver the updated session state incrementally.
    } catch (e) {
      setPermissionError(e instanceof Error ? e.message : 'Failed to respond to permission request');
    } finally {
      setAnsweringPermission(false);
    }
  }, [answeringPermission, caps.respondPermission, pendingPermission, portAvailable, respondPermission, session, setPendingPermission]);

  const handleQuestionReply = useCallback(async (answers: string[][]) => {
    if (!pendingQuestion || answeringQuestion || !portAvailable || !caps.respondQuestion || !session) return;
    setQuestionError(null);
    setAnsweringQuestion(true);
    try {
      await respondQuestion(session.id, pendingQuestion.requestId, answers);
      setPendingQuestion(null);
      setQuestionError(null);
      clearPendingQuestion(session.id);
      notifyPromptDismissed(session.id);
    } catch (e) {
      console.error('Failed to respond to question', e);
      setQuestionError(e instanceof Error ? e.message : 'Failed to submit answer');
    } finally {
      setAnsweringQuestion(false);
    }
  }, [answeringQuestion, caps.respondQuestion, pendingQuestion, portAvailable, respondQuestion, session, setPendingQuestion]);

  const handleQuestionReject = useCallback(async () => {
    if (!pendingQuestion || answeringQuestion || !portAvailable || !caps.respondQuestion || !session) return;
    setAnsweringQuestion(true);
    try {
      await rejectQuestion(session.id, pendingQuestion.requestId);
      setPendingQuestion(null);
      clearPendingQuestion(session.id);
      notifyPromptDismissed(session.id);
    } catch (e) {
      console.error('Failed to dismiss question', e);
    } finally {
      setAnsweringQuestion(false);
    }
  }, [answeringQuestion, caps.respondQuestion, pendingQuestion, portAvailable, rejectQuestion, session, setPendingQuestion]);

  return {
    answeringPermission,
    permissionError,
    setPermissionError,
    answeringQuestion,
    questionError,
    setQuestionError,
    handlePermissionReply,
    handleQuestionReply,
    handleQuestionReject,
  };
}
