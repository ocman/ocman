// usePendingPromptSync — two-way sync between the sidebar session list and the
// session-detail's pending-prompt state.
//
// Forward (detail → sidebar): mirrors the current session's pendingPermission /
// pendingQuestion flags into its sidebar row so the badge lights up/clears
// immediately from SSE, without waiting for the 10-second background poll.
//
// Reverse (sidebar → detail): when the sidebar poll discovers a prompt the
// detail view doesn't know about (missed SSE event), fetches the full
// permission/question data so the dialog appears.

import { useEffect } from 'react';
import { useApiStore } from '../../lib/apiStore';
import { extractPendingPermission, extractPendingQuestion } from '../../lib/sseHelpers';
import { storePendingQuestion } from './usePromptHandlers';
import { isSessionRelevant } from '../../lib/promptRouting';
import type { Session } from '../../lib/api';
import type { PendingPermission } from '../../lib/sseHelpers';
import type { PendingQuestion } from '../../components/session/QuestionPrompt';

interface PendingPromptSyncOptions {
  id: string | undefined;
  pendingPermission: PendingPermission | null;
  pendingQuestion: PendingQuestion | null;
  setPendingPermission: (p: PendingPermission | null) => void;
  setPendingQuestion: React.Dispatch<React.SetStateAction<PendingQuestion | null>>;
  setPermissionError: (e: string | null) => void;
  recentSessions: Session[];
  subagentSessionIdsRef: React.MutableRefObject<Set<string>>;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  listPermissions: (id: string) => Promise<any[]>;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  listQuestions: (id: string) => Promise<any[]>;
}

export function usePendingPromptSync({
  id,
  pendingPermission,
  pendingQuestion,
  setPendingPermission,
  setPendingQuestion,
  setPermissionError,
  recentSessions,
  subagentSessionIdsRef,
  listPermissions,
  listQuestions,
}: PendingPromptSyncOptions): void {
  const patchRecentSession = useApiStore((s) => s.patchRecentSession);

  // Forward: detail → sidebar badge.
  useEffect(() => {
    if (!id) return;
    patchRecentSession(id, {
      pendingPermission: pendingPermission !== null,
      pendingQuestion: pendingQuestion !== null,
    });
  }, [id, pendingPermission, pendingQuestion, patchRecentSession]);

  // Reverse: sidebar poll → detail prompt dialog.
  const sidebarCurrentSession = recentSessions.find(s => s.id === id);
  const sidebarHasPerm = sidebarCurrentSession?.pendingPermission ?? false;
  const sidebarHasQuestion = sidebarCurrentSession?.pendingQuestion ?? false;

  useEffect(() => {
    if (!id) return;
    if (sidebarHasPerm && pendingPermission === null) {
      listPermissions(id)
        .then(perms => {
          for (const p of perms) {
            const perm = extractPendingPermission({ type: 'permission.asked', properties: p });
            if (!perm) continue;
            const promptSid = typeof p['sessionID'] === 'string' ? p['sessionID'] : '';
            if (!isSessionRelevant(promptSid, id, subagentSessionIdsRef.current)) continue;
            setPendingPermission(perm);
            setPermissionError(null);
            break;
          }
        })
        .catch(() => { /* sidebar will retry on next poll */ });
    }
    if (sidebarHasQuestion && pendingQuestion === null) {
      listQuestions(id)
        .then(questions => {
          for (const q of questions) {
            const question = extractPendingQuestion({ type: 'question.asked', properties: q });
            if (!question) continue;
            const questionSid = typeof q['sessionID'] === 'string' ? q['sessionID'] : '';
            if (!isSessionRelevant(questionSid, id, subagentSessionIdsRef.current)) continue;
            storePendingQuestion(id, question);
            setPendingQuestion((prev: PendingQuestion | null) => prev ?? question);
            break;
          }
        })
        .catch(() => { /* sidebar will retry on next poll */ });
    }
  }, [id, sidebarHasPerm, sidebarHasQuestion, pendingPermission, pendingQuestion,
    listPermissions, listQuestions, setPermissionError, setPendingPermission,
    setPendingQuestion, subagentSessionIdsRef]);
}
