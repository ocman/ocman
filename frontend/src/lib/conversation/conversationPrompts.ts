// Prompt folding for the conversation reducer: OCE prompts drive the view's
// single pending permission and question plus the auto-approve judge state.

import type { PendingQuestion } from '../../components/session/QuestionPrompt';
import type { PendingPermission } from '../sseHelpers';
import type { SessionView } from '../sessionViewTypes';
import type { OcePrompt, PromptJudge, PromptResolved } from './events';

function toPermission(prompt: OcePrompt): PendingPermission {
  const req = prompt.permission;
  return {
    permissionId: prompt.id,
    permission: req?.permission || 'Permission required',
    patterns: req?.patterns ?? [],
    ...(req?.always ? { always: req.always } : {}),
    ...(req?.metadata ? { metadata: req.metadata } : {}),
    sessionId: prompt.sessionId,
    askedAt: prompt.askedAt,
  };
}

function toQuestion(prompt: OcePrompt): PendingQuestion | null {
  const questions = prompt.question?.questions ?? [];
  if (!questions.length) return null;
  return { requestId: prompt.id, sessionID: prompt.sessionId, questions };
}

export function askPrompt(view: SessionView, prompt: OcePrompt): SessionView {
  if (prompt.kind === 'permission') {
    if (view.pendingPermission?.permissionId === prompt.id) return view;
    return { ...view, pendingPermission: toPermission(prompt), judgeStartsAt: null, judgeReasoning: null };
  }
  if (view.pendingQuestion?.requestId === prompt.id) return view;
  const question = toQuestion(prompt);
  return question ? { ...view, pendingQuestion: question } : view;
}

export function judgePrompt(view: SessionView, judge: PromptJudge): SessionView {
  // A judge frame may race its prompt; it applies unless another prompt is shown.
  const other = !!view.pendingPermission && view.pendingPermission.permissionId !== judge.promptId;
  if (other && judge.phase !== 'checking') return view;
  switch (judge.phase) {
    case 'scheduled':
      if (typeof judge.startsAt !== 'number' || view.judgeStartsAt === judge.startsAt) return view;
      return { ...view, judgeStartsAt: judge.startsAt };
    case 'checking':
      if (view.checkingPermissionId === judge.promptId) return view;
      return { ...view, judgeStartsAt: null, checkingPermissionId: judge.promptId };
    case 'flagged':
      if (!judge.reasoning) return view;
      return {
        ...view,
        checkingPermissionId: view.checkingPermissionId === judge.promptId ? null : view.checkingPermissionId,
        judgeReasoning: judge.reasoning,
      };
    default:
      return view;
  }
}

export function resolvePrompt(view: SessionView, resolved: PromptResolved): SessionView {
  const id = resolved.promptId;
  if (resolved.kind === 'question') {
    return view.pendingQuestion?.requestId === id ? { ...view, pendingQuestion: null } : view;
  }
  const checkingPermissionId = view.checkingPermissionId === id ? null : view.checkingPermissionId;
  // Judge state belongs to the shown prompt; with none shown it may be a late frame for this one.
  if (!view.pendingPermission || view.pendingPermission.permissionId === id) {
    if (!view.pendingPermission && view.judgeStartsAt === null && view.judgeReasoning === null
      && checkingPermissionId === view.checkingPermissionId) return view;
    return { ...view, pendingPermission: null, judgeStartsAt: null, judgeReasoning: null, checkingPermissionId };
  }
  return checkingPermissionId === view.checkingPermissionId ? view : { ...view, checkingPermissionId };
}

/** Restate the open prompts and judge phases from a hello overlay (absolute). */
export function applyPromptOverlay(view: SessionView, prompts: OcePrompt[], judge: PromptJudge[]): SessionView {
  const permissions = prompts.filter((p) => p.kind === 'permission');
  const questions = prompts.filter((p) => p.kind === 'question');
  const keptPermission = permissions.find((p) => p.id === view.pendingPermission?.permissionId);
  const keptQuestion = questions.find((p) => p.id === view.pendingQuestion?.requestId);
  const permission = keptPermission ?? permissions[0];
  const question = keptQuestion ?? questions[0];
  let next: SessionView = {
    ...view,
    pendingPermission: keptPermission ? view.pendingPermission : permission ? toPermission(permission) : null,
    pendingQuestion: keptQuestion ? view.pendingQuestion : question ? toQuestion(question) : null,
  };
  if (!keptPermission) next = { ...next, judgeStartsAt: null, judgeReasoning: null, checkingPermissionId: null };
  for (const phase of judge) next = judgePrompt(next, phase);
  return next;
}
