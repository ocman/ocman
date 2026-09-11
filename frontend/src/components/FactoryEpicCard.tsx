// Renders a Factory epic link written by an agent as a live status card.
// Inline elements only: react-markdown places links inside a paragraph.
import { Link } from 'react-router-dom';
import type { ReactNode } from 'react';
import { useWorkEpic } from '../lib/queries';
import { factoryEpicStatus } from './factoryEpicStatus';
import './FactoryEpicCard.css';

export function FactoryEpicCard({ epicID, children }: { epicID: string; children?: ReactNode }) {
  const epic = useWorkEpic(epicID);
  const to = `/factory/epics/${encodeURIComponent(epicID)}`;
  // Until the epic resolves the card is just the link the agent wrote.
  if (!epic.data) return <Link to={to}>{children}</Link>;
  const status = factoryEpicStatus(epic.data);
  return <span className="oc-epic-card" data-testid={`epic-card-${epicID}`}>
    <Link className="oc-epic-card-goal" to={to}>{epic.data.goal || children}</Link>
    <span className={`oc-epic-card-status oc-epic-card-status--${status.tone}`}>{status.text}</span>
    <span className="oc-epic-card-id">{epicID} · {epic.data.initialProject}</span>
  </span>;
}
