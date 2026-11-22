import { createContext, useContext } from 'react';

export type ActivitySlice = 'inline' | 'work';
export const ActivitySliceContext = createContext<ActivitySlice | null>(null);
export const useActivitySlice = () => useContext(ActivitySliceContext);

export function findActivityTarget(viewport: HTMLElement, messageId: string, toolCallId?: string) {
  const id = CSS.escape(messageId);
  const inline = `[data-message-id="${id}"]:not([data-activity-message-id])`;
  const work = `[data-activity-message-id="${id}"], details.oc-agent-activity [data-message-id="${id}"]`;
  const tool = toolCallId ? ` [data-tool-call-id="${CSS.escape(toolCallId)}"]` : '';
  const target = viewport.querySelector<HTMLElement>(`${inline}${tool}`)
    ?? viewport.querySelector<HTMLElement>(`:is(${work})${tool}`);
  target?.closest('details.oc-agent-activity')?.setAttribute('open', '');
  return target;
}
