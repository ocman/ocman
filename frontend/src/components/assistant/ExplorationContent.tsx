import { useState, type ComponentProps } from 'react';
import { MessagePrimitive, useAuiState } from '@assistant-ui/react';
import { explorationKind, explorationRunning, messageExploration, type ExplorationEntry, type ExplorationPart } from '../../lib/exploration';
import { useUiStore } from '../../lib/uiStore';
import { parseToolTime } from '../../lib/threadHelpers';
import { useIsPrinting } from '../../lib/useIsPrinting';
import { usePrintCollapse } from '../../lib/printCollapseContext';
import './ExplorationContent.css';

function ExplorationGroup({ entries }: { entries: ExplorationEntry[] }) {
  const [open, setOpen] = useState(false);
  const printing = useIsPrinting();
  const printCollapse = usePrintCollapse();
  const searches = entries.filter(entry => explorationKind(entry.part) === 'search').length;
  const files = entries.length - searches;
  const running = entries.some(entry => explorationRunning(entry.part));
  const messages = new Map<string, ExplorationPart[]>();
  for (const { messageId, part } of entries) {
    const parts = messages.get(messageId) ?? [];
    parts.push(part);
    messages.set(messageId, parts);
  }
  return (
    <details className="oc-exploration" open={open || (printing && !printCollapse)} onToggle={event => { if (!printing) setOpen(event.currentTarget.open); }}>
      <summary className="oc-tool-compact-line">
        <span className="oc-read-arrow" aria-hidden="true">{running ? '◌' : '→'}</span>
        <span>{running ? 'Exploring' : 'Explored'}: {searches} {searches === 1 ? 'search' : 'searches'} {files} {files === 1 ? 'file' : 'files'}</span>
      </summary>
      <div className="oc-exploration-children">
        {[...messages].map(([messageId, parts]) => (
          <div key={messageId} data-message-id={messageId}>
            {parts.map(part => <div key={part.toolCallId} className="oc-tool-source" data-tool-call-id={part.toolCallId}>
              <div className="oc-read-line">
                <span className="oc-read-arrow" aria-hidden="true">└</span>
                <span>{parseToolTime(part.argsText)?.strippedArgs || part.argsText}</span>
              </div>
            </div>)}
          </div>
        ))}
      </div>
    </details>
  );
}

/** Keep normal part contexts for all non-exploration content. */
export function ExplorationContent({ components }: Pick<ComponentProps<typeof MessagePrimitive.Content>, 'components'>) {
  const compact = useUiStore(state => state.compactToolCalls);
  return compact ? <GroupedExplorationContent components={components} /> : <MessagePrimitive.Content components={components} />;
}

function GroupedExplorationContent({ components }: Pick<ComponentProps<typeof MessagePrimitive.Content>, 'components'>) {
  const message = useAuiState(state => state.message);
  const messages = useAuiState(state => state.thread.messages);
  const entries = messageExploration(messages, message.id);
  if (entries) return entries.length ? <ExplorationGroup entries={entries} /> : null;

  const parts = [];
  for (let i = 0; i < message.content.length; i++) {
    const part = message.content[i];
    if (part.type !== 'tool-call' || !explorationKind(part)) {
      parts.push(<MessagePrimitive.PartByIndex key={i} index={i} components={components} />);
      continue;
    }
    const group: ExplorationEntry[] = [{ messageId: message.id, part }];
    while (i + 1 < message.content.length) {
      const next = message.content[i + 1];
      if (next.type !== 'tool-call' || !explorationKind(next)) break;
      group.push({ messageId: message.id, part: next });
      i++;
    }
    parts.push(<ExplorationGroup key={part.toolCallId} entries={group} />);
  }
  return <>{parts}</>;
}
