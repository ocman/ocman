import { useEffect, type CSSProperties } from 'react';
import type { BeadsStatus, BeadsTicket } from '../lib/beadsApi';

type BeadsPaneProps = {
  status: BeadsStatus;
  loading: boolean;
  error: Error | null;
  refresh: () => unknown;
  onRefresh?: (refresh: () => void) => void;
  onLoadingChange?: (loading: boolean) => void;
};

export function BeadsPane({
  status,
  loading,
  error,
  refresh,
  onRefresh,
  onLoadingChange,
}: BeadsPaneProps) {
  useEffect(() => onRefresh?.(() => { void refresh(); }), [onRefresh, refresh]);
  useEffect(() => onLoadingChange?.(loading), [loading, onLoadingChange]);

  const tickets = status.tickets ?? [];
  const ids = new Set(tickets.map((ticket) => ticket.id));
  const children = new Map<string, BeadsTicket[]>();
  for (const ticket of tickets) {
    if (!ticket.parentId || !ids.has(ticket.parentId)) continue;
    children.set(ticket.parentId, [...(children.get(ticket.parentId) ?? []), ticket]);
  }
  const roots = tickets.filter((ticket) => !ticket.parentId || !ids.has(ticket.parentId));
  const unhealthy = !!status.error || !!error;

  const renderTicket = (
    ticket: BeadsTicket,
    ancestors: Set<string>,
    depth: number,
    ancestorContinues: boolean[],
    isLast: boolean,
  ) => {
    const childTickets = ancestors.has(ticket.id) ? [] : (children.get(ticket.id) ?? []);
    const trunk = depth * 12;
    return (
    <li key={ticket.id}>
      <div className="oc-beads-ticket" style={{ '--oc-beads-marker-width': `${trunk + 17}px` } as CSSProperties}>
        <span className="oc-beads-marker">
          {ancestorContinues.map((continues, level) => continues && (
            <span key={level} className="oc-beads-guide" style={{ left: `${level * 12}px` }} aria-hidden="true" />
          ))}
          <span className={`oc-beads-branch${isLast ? '' : ' continues'}`} style={{ left: `${trunk}px` }} aria-hidden="true" />
          {childTickets.length > 0 && (
            <span className="oc-beads-child-bridge" style={{ left: `${trunk + 12}px` }} aria-hidden="true" />
          )}
          <span
            className={`oc-beads-status ${ticket.status}`}
            style={{ left: `${trunk + 7}px` }}
            aria-label={ticket.status.replace('_', ' ')}
          />
        </span>
        <div className="oc-beads-content">
          <div className="oc-beads-main">
            <span className="oc-beads-priority">P{ticket.priority}</span>
            <span className="oc-beads-title">{ticket.title}</span>
          </div>
          <div className="oc-beads-details">
            {ticket.issueType && (
              <span className="oc-beads-type">[{ticket.issueType}]</span>
            )}
            <code className="oc-beads-id">{ticket.id}</code>
          </div>
        </div>
      </div>
      {childTickets.length > 0 && (
        <ul>
          {childTickets.map((child, index) => renderTicket(
            child,
            new Set([...ancestors, ticket.id]),
            depth + 1,
            [...ancestorContinues, !isLast],
            index === childTickets.length - 1,
          ))}
        </ul>
      )}
    </li>
    );
  };

  return (
    <div className="oc-beads-pane">
      {unhealthy && (
        <div className="oc-beads-error" role="alert">
          <span>Could not refresh Beads tickets.</span>
          <button type="button" onClick={() => void refresh()} aria-label="Retry Beads tickets">Retry</button>
        </div>
      )}
      {tickets.length === 0 && !unhealthy ? (
        <p className="oc-beads-empty">No active tickets.</p>
      ) : tickets.length > 0 ? (
        <ul className="oc-beads-tree" aria-label="Beads tickets">
          {roots.map((ticket, index) => renderTicket(ticket, new Set(), 0, [], index === roots.length - 1))}
        </ul>
      ) : null}
    </div>
  );
}
