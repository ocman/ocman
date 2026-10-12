import type { Message, Part, SessionDetail } from '../../lib/api';
import { initialSessionView, type SessionView } from '../../lib/sessionReducer';

export function viewFromDetail(id: string, detail: SessionDetail): SessionView {
  let messages = detail.messages ?? [];
  let parts = detail.parts ?? [];
  const notice = detail.session.notice;
  if (notice) {
    const noticeID = `ocman-session-notice-${id}`;
    const timeCreated = detail.session.timeUpdated || Date.now();
    const noticeMsg: Message = { id: noticeID, sessionId: id, timeCreated, data: { role: 'notice' } };
    const noticePart: Part = {
      id: `${noticeID}-part`, messageId: noticeID, sessionId: id, timeCreated,
      data: { type: 'text', text: notice.message },
    };
    messages = [...messages.filter((m) => m.id !== noticeID), noticeMsg];
    parts = [...parts.filter((p) => p.messageId !== noticeID), noticePart];
  }
  return {
    ...initialSessionView(id),
    session: {
      ...detail.session,
      contextTokenCount: detail.session.contextTokenCount ?? detail.contextTokenCount,
      defaultAgent: detail.defaultAgent,
      defaultModel: detail.defaultModel,
      warnings: detail.warnings ?? [],
    },
    sessionTree: detail.sessionTree ?? [], messages, parts,
  };
}

/** Cache only the latest page, regardless of how far the user scrolled back. */
export function latestPage(messages: Message[], parts: Part[], keep: number): { messages: Message[]; parts: Part[] } {
  if (messages.length <= keep) return { messages, parts };
  const kept = messages.slice(-keep);
  const ids = new Set(kept.map((m) => m.id));
  return { messages: kept, parts: parts.filter((p) => ids.has(p.messageId)) };
}

export function platformMessageCount(messages: Message[]): number {
  return messages.reduce((count, message) => count + (message.data?.role === 'notice' ? 0 : 1), 0);
}

/** Restoring an existing browser view is a read, not an explicit reopen. */
export function isSessionPageRestore(sessionId?: string): boolean {
  const navigation = performance.getEntriesByType?.('navigation')[0] as PerformanceNavigationTiming | undefined;
  if (navigation?.type !== 'reload' && navigation?.type !== 'back_forward') return false;
  // Navigation timing describes the document's original URL, even after SPA navigation.
  return !navigation.name || new URL(navigation.name).pathname === `/session/${encodeURIComponent(sessionId ?? '')}`;
}
