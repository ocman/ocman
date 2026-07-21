import { memo, useState } from 'react';
import { parseChildMessage } from '../../lib/convertMessages';

export interface QueuedMessageItem {
  id: string;
  text: string;
  hasImages: boolean;
  canMove?: boolean;
  removeLabel?: string;
}

/**
 * The follow-up message queue shown under the composer (#58): prompts the
 * user submitted while the agent was mid-turn, waiting to send one per
 * turn on the next idle edge. Pure presentational — the queue state and
 * mutations live in the parent (useMessageQueue); this only renders the
 * list and forwards remove / reorder intents.
 *
 * Each item's text is clamped to 3 lines so a long prompt can't take over
 * the view; clicking the text toggles the full body.
 */
function QueuedMessagesImpl({
  messages,
  onRemove,
  onMove,
}: {
  messages: QueuedMessageItem[];
  onRemove?: (id: string) => void;
  onMove?: (id: string, direction: -1 | 1) => void;
}) {
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  if (messages.length === 0) return null;
  const toggle = (id: string) =>
    setExpanded((prev) => ({ ...prev, [id]: !prev[id] }));
  return (
    <ul
      className="oc-queued-messages"
      data-testid="queued-messages"
      aria-label="Queued follow-up messages"
    >
      {messages.map((m, i) => {
        const childMessage = parseChildMessage(m.text);
        return (
          <li key={m.id} className={`oc-queued-message${childMessage ? ' oc-queued-agent-message' : ''}`}>
            <i className="bi bi-hourglass-split oc-queued-icon" aria-hidden="true" />
            <button
              type="button"
              className={`oc-queued-text${expanded[m.id] ? ' oc-queued-text-expanded' : ''}`}
              title={expanded[m.id] ? 'Collapse' : (childMessage?.content || m.text)}
              aria-expanded={!!expanded[m.id]}
              onClick={() => toggle(m.id)}
            >
              {m.hasImages && <i className="bi bi-image oc-queued-image" aria-hidden="true" />}
              {childMessage ? (
                <>
                  <span className="oc-queued-agent-header">
                    <strong>Agent update</strong>
                    {childMessage.intent && <span>{childMessage.intent}</span>}
                    {childMessage.status && <span className="oc-queued-agent-status">{childMessage.status}</span>}
                  </span>
                  <span>{childMessage.content}</span>
                </>
              ) : m.text || (m.hasImages ? '(image)' : '')}
            </button>
            <span className="oc-queued-actions">
              <button
                type="button"
                className="oc-queued-btn"
                disabled={m.canMove === false || i === 0 || messages[i - 1]?.canMove === false}
                onClick={() => onMove?.(m.id, -1)}
                title="Move up"
                aria-label="Move up"
              ><i className="bi bi-arrow-up" aria-hidden="true" /></button>
              <button
                type="button"
                className="oc-queued-btn"
                disabled={m.canMove === false || i === messages.length - 1 || messages[i + 1]?.canMove === false}
                onClick={() => onMove?.(m.id, 1)}
                title="Move down"
                aria-label="Move down"
              ><i className="bi bi-arrow-down" aria-hidden="true" /></button>
              <button
                type="button"
                className="oc-queued-btn oc-queued-remove"
                onClick={() => onRemove?.(m.id)}
                title={m.removeLabel || 'Remove from queue'}
                aria-label={m.removeLabel || 'Remove from queue'}
              >{'\u00D7'}</button>
            </span>
          </li>
        );
      })}
    </ul>
  );
}

export const QueuedMessages = memo(QueuedMessagesImpl);
