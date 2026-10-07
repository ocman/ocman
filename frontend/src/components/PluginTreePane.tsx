import { useEffect, type CSSProperties } from 'react';
import type { PluginPaneTree, PluginTreeNode } from '../lib/pluginPanes';
import { Button } from './Control';
import './PluginTreePane.css';

type PluginTreePaneProps = {
  status: PluginPaneTree;
  label: string;
  loading: boolean;
  error: Error | null;
  refresh: () => unknown;
  onRefresh?: (refresh: () => void) => void;
  onLoadingChange?: (loading: boolean) => void;
};

export function PluginTreePane({
  status,
  label,
  loading,
  error,
  refresh,
  onRefresh,
  onLoadingChange,
}: PluginTreePaneProps) {
  useEffect(() => onRefresh?.(() => { void refresh(); }), [onRefresh, refresh]);
  useEffect(() => onLoadingChange?.(loading), [loading, onLoadingChange]);

  const tickets = status.nodes ?? [];
  const ids = new Set(tickets.map((ticket) => ticket.id));
  const children = new Map<string, PluginTreeNode[]>();
  for (const ticket of tickets) {
    if (!ticket.parentId || !ids.has(ticket.parentId)) continue;
    children.set(ticket.parentId, [...(children.get(ticket.parentId) ?? []), ticket]);
  }
  const roots = tickets.filter((ticket) => !ticket.parentId || !ids.has(ticket.parentId));
  const unhealthy = !!status.warning || !!error;

  const renderTicket = (
    ticket: PluginTreeNode,
    ancestors: Set<string>,
    depth: number,
    ancestorContinues: boolean[],
    isLast: boolean,
  ) => {
    const childTickets = ancestors.has(ticket.id) ? [] : (children.get(ticket.id) ?? []);
    const trunk = depth * 12;
    return (
    <li key={ticket.id}>
      <div className="oc-plugin-tree-node" style={{ '--oc-plugin-tree-marker-width': `${trunk + 17}px` } as CSSProperties}>
        <span className="oc-plugin-tree-marker">
          {ancestorContinues.map((continues, level) => continues && (
            <span key={level} className="oc-plugin-tree-guide" style={{ left: `${level * 12}px` }} aria-hidden="true" />
          ))}
          <span className={`oc-plugin-tree-branch${isLast ? '' : ' continues'}`} style={{ left: `${trunk}px` }} aria-hidden="true" />
          {childTickets.length > 0 && (
            <span className="oc-plugin-tree-child-bridge" style={{ left: `${trunk + 12}px` }} aria-hidden="true" />
          )}
          <span
            className={`oc-plugin-tree-status ${ticket.status}`}
            style={{ left: `${trunk + 7}px` }}
              aria-label={ticket.status?.replace('_', ' ')}
          />
        </span>
        <div className="oc-plugin-tree-content">
          <div className="oc-plugin-tree-main">
            {ticket.badge && <span className="oc-plugin-tree-badge">{ticket.badge}</span>}
            <span className="oc-plugin-tree-title">{ticket.title}</span>
          </div>
          <div className="oc-plugin-tree-details">
            {ticket.kind && (
              <span className="oc-plugin-tree-kind">[{ticket.kind}]</span>
            )}
            <code className="oc-plugin-tree-id">{ticket.id}</code>
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
    <div className="oc-plugin-tree-pane">
      {unhealthy && (
        <div className="oc-plugin-tree-error" role="alert">
          <span>Could not refresh {label}.</span>
          <Button variant="ghost" onClick={() => void refresh()} aria-label={`Retry ${label}`}>Retry</Button>
        </div>
      )}
      {tickets.length === 0 && !unhealthy ? (
        <p className="oc-plugin-tree-empty">{status.available ? 'No active items.' : 'This pane is unavailable for this project.'}</p>
      ) : tickets.length > 0 ? (
        <ul className="oc-plugin-tree-list" aria-label={`${label} tree`}>
          {roots.map((ticket, index) => renderTicket(ticket, new Set(), 0, [], index === roots.length - 1))}
        </ul>
      ) : null}
    </div>
  );
}
