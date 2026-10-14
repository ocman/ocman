// Permission and question prompt handlers for the SessionDetail reducer.

import type { SessionView, SseEvent } from './sessionViewTypes';
import { extractPendingPermission, extractPendingQuestion } from './sseHelpers';
import { upsertMessage } from './sessionViewParts';

export function reducePermissionAsked(state: SessionView, event: SseEvent): SessionView {
  const perm = extractPendingPermission({
    type: event.type,
    properties: event.properties,
  });
  if (!perm) return state;
  if (state.pendingPermission && state.pendingPermission.permissionId === perm.permissionId) {
    return state;
  }
  // New permission: clear any stale judge state from the previous prompt.
  return {
    ...state,
    pendingPermission: perm,
    judgeStartsAt: null,
    judgeReasoning: null,
  };
}

export function reducePermissionReplied(state: SessionView, props: Record<string, unknown>): SessionView {
  if (!state.pendingPermission) return state;
  const repliedId =
    (typeof props.requestID === 'string' && props.requestID) ||
    (typeof props.requestId === 'string' && props.requestId) ||
    (typeof props.id === 'string' && props.id) ||
    (typeof props.permissionID === 'string' && props.permissionID) ||
    '';
  if (!repliedId || state.pendingPermission.permissionId !== repliedId) return state;
  return {
    ...state,
    pendingPermission: null,
    judgeStartsAt: null,
    checkingPermissionId: state.checkingPermissionId === repliedId ? null : state.checkingPermissionId,
    judgeReasoning: null,
  };
}

/**
 * Fired by the backend immediately after a permission.asked event when
 * auto-approve is enabled. Carries `judgeStartsAt` (Unix-ms) so the UI
 * can show a live countdown without any local timer state.
 */
export function reducePermissionPending(state: SessionView, props: Record<string, unknown>): SessionView {
  const permId = typeof props.permissionId === 'string' ? props.permissionId : null;
  const judgeStartsAt = typeof props.judgeStartsAt === 'number' ? props.judgeStartsAt : null;
  if (!permId || judgeStartsAt === null) return state;
  // Accept even if pendingPermission isn't set yet — the event may arrive
  // before or after permission.asked depending on goroutine scheduling.
  // Only apply if the permission IDs match when pendingPermission is set.
  if (state.pendingPermission !== null && state.pendingPermission.permissionId !== permId) return state;
  if (state.judgeStartsAt === judgeStartsAt) return state;
  return { ...state, judgeStartsAt };
}

/**
 * Fired by the backend when the LLM judge has started evaluating a
 * permission. Sets `checkingPermissionId` so the UI can show a spinner.
 */
export function reducePermissionChecking(state: SessionView, props: Record<string, unknown>): SessionView {
  const permId = typeof props.permissionId === 'string' ? props.permissionId : null;
  if (!permId) return state;
  if (state.checkingPermissionId === permId) return state;
  // Countdown is over — clear judgeStartsAt and set checking.
  return { ...state, judgeStartsAt: null, checkingPermissionId: permId };
}

/**
 * Fired by the backend when the LLM judge returned an unsafe verdict and
 * handed the permission back to the human. Stores the one-line reasoning
 * so the prompt can show *why* the AI flagged it.
 */
export function reducePermissionFlagged(state: SessionView, props: Record<string, unknown>): SessionView {
  const permId = typeof props.permissionId === 'string' ? props.permissionId : null;
  const reasoning = typeof props.reasoning === 'string' && props.reasoning
    ? props.reasoning
    : null;
  // Need at least one of the two to be useful. The judge session ID
  // used to be required (and rendered as a magnifying-glass link), but
  // those sessions are now deleted post-verdict — reasoning is the only
  // surviving signal.
  if (!permId || !reasoning) return state;
  // Apply if the permission IDs match (or pendingPermission is null — the
  // event may arrive just as the user manually replied).
  if (state.pendingPermission !== null && state.pendingPermission.permissionId !== permId) return state;
  return {
    ...state,
    checkingPermissionId: state.checkingPermissionId === permId ? null : state.checkingPermissionId,
    judgeReasoning: reasoning,
  };
}

/**
 * Fired by the backend when the LLM judge approved a permission.
 * Injects the approval notice into the thread and clears the pending
 * permission prompt and checking indicator.
 */
export function reducePermissionAutoApproved(state: SessionView, props: Record<string, unknown>): SessionView {
  const permId = typeof props.permissionId === 'string' ? props.permissionId : '';
  const permission = typeof props.permission === 'string' ? props.permission : '';
  const patterns = Array.isArray(props.patterns)
    ? (props.patterns as unknown[]).filter((p): p is string => typeof p === 'string')
    : [];
  const reasoning = typeof props.reasoning === 'string' ? props.reasoning : '';
  const approvedBy = props.approvedBy === 'user' ? 'user' : 'ai';
  const reply = props.reply === 'always' ? 'always' : props.reply === 'once' ? 'once' : undefined;
  const metadata = props.metadata && typeof props.metadata === 'object' && !Array.isArray(props.metadata)
    ? props.metadata as Record<string, unknown>
    : undefined;
  const askedAt = typeof props.askedAt === 'number' ? props.askedAt : undefined;
  const approvedAt = typeof props.approvedAt === 'number' ? props.approvedAt : Date.now();

  if (!permId || !permission) return state;

  // Inject the approval notice. Stable key uses the permission ID so
  // repeated deliveries (SSE reconnect + initial GET re-injection)
  // dedupe deterministically.
  const stableKey = `ocman-notice-${permId}`;
  let messages = state.messages;
  let parts = state.parts;
  if (!messages.some((m) => m.id === stableKey)) {
    const noticeMsg: import('./api').Message = {
      id: stableKey,
      sessionId: state.sessionId,
      timeCreated: approvedAt,
      data: { role: 'notice' },
    };
    const noticePart: import('./api').Part = {
      id: `${stableKey}-part`,
      messageId: stableKey,
      sessionId: state.sessionId,
      timeCreated: approvedAt,
      data: JSON.stringify({
        type: 'auto-approved',
        permission,
        patterns,
        reasoning,
        approvedBy,
        reply,
        metadata,
        askedAt,
        approvedAt,
      }),
    };
    messages = upsertMessage(messages, noticeMsg);
    parts = [...parts, noticePart];
  }

  // Clear the pending permission if it matches.
  const pendingPermission =
    state.pendingPermission?.permissionId === permId ? null : state.pendingPermission;

  return {
    ...state,
    messages,
    parts,
    pendingPermission,
    judgeStartsAt: pendingPermission === null ? null : state.judgeStartsAt,
    checkingPermissionId: state.checkingPermissionId === permId ? null : state.checkingPermissionId,
    judgeReasoning: pendingPermission === null ? null : state.judgeReasoning,
  };
}

export function reduceQuestionAsked(state: SessionView, event: SseEvent): SessionView {
  const q = extractPendingQuestion({
    type: event.type,
    properties: event.properties,
  });
  if (!q) return state;
  if (state.pendingQuestion && state.pendingQuestion.requestId === q.requestId) return state;
  return { ...state, pendingQuestion: q };
}

export function reduceQuestionCleared(state: SessionView, props: Record<string, unknown>): SessionView {
  if (!state.pendingQuestion) return state;
  const repliedId =
    (typeof props.requestID === 'string' && props.requestID) ||
    (typeof props.requestId === 'string' && props.requestId) ||
    (typeof props.id === 'string' && props.id) ||
    '';
  if (!repliedId || state.pendingQuestion.requestId !== repliedId) return state;
  return { ...state, pendingQuestion: null };
}
