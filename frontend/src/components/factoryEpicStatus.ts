import type { FactoryEpic } from '../lib/api';

const EPIC_PATH = /^\/factory\/epics\/([^/?#]+)$/;

export function factoryEpicIDFromHref(href?: string) {
  return href ? EPIC_PATH.exec(href)?.[1] : undefined;
}

// One line of live state for an epic: what needs a human beats what is merely progressing.
export function factoryEpicStatus(epic: FactoryEpic): { text: string; tone: 'action' | 'done' | 'idle' } {
  switch (epic.planGate?.resolution) {
    case 'open': return { text: 'Plan awaiting your approval', tone: 'action' };
    case 'revision_requested': return { text: 'Plan revision requested', tone: 'action' };
    case 'rejected': return { text: 'Plan rejected', tone: 'idle' };
    default: break;
  }
  if (epic.status === 'closed') return { text: 'Closed', tone: 'done' };
  if (epic.status === 'paused') return { text: 'Paused', tone: 'idle' };
  if (epic.progress?.stuck) return { text: 'Stuck: nothing can proceed', tone: 'action' };
  const { requiredSucceeded, requiredTotal, optionalOpen } = epic.progress ?? { requiredSucceeded: 0, requiredTotal: 0, optionalOpen: 0 };
  if (requiredTotal) return { text: `${requiredSucceeded}/${requiredTotal} required work complete${optionalOpen ? `, ${optionalOpen} optional open` : ''}`, tone: requiredSucceeded === requiredTotal ? 'done' : 'idle' };
  return { text: 'Open', tone: 'idle' };
}
