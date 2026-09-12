// Renders a Factory epic link written by an agent as a live status card.
// Inline elements only: react-markdown places links inside a paragraph.
import { Link, useNavigate } from 'react-router-dom';
import type { ReactNode } from 'react';
import { useClaimFactoryPlan, useFactoryIssues, useWorkEpic } from '../lib/queries';
import { Button } from './Control';
import { factoryEpicStatus } from './factoryEpicStatus';
import './FactoryEpicCard.css';

export function FactoryEpicCard({ epicID, children }: { epicID: string; children?: ReactNode }) {
  const epic = useWorkEpic(epicID);
  const issues = useFactoryIssues(epic.data?.status === 'open' ? epicID : '');
  const claim = useClaimFactoryPlan(epicID);
  const navigate = useNavigate();
  const to = `/factory/epics/${encodeURIComponent(epicID)}`;
  // Until the epic resolves the card is just the link the agent wrote.
  if (!epic.data) return <Link to={to}>{children}</Link>;
  const status = factoryEpicStatus(epic.data);
  const readyPlans = epic.data.status === 'open' ? issues.data?.filter((issue) => issue.kind === 'plan' && issue.dispatchState === 'ready' && !epic.data.attempts?.some((attempt) => attempt.workId === issue.id && ['prepared', 'active', 'stopping'].includes(attempt.phase))) ?? [] : [];
  return <span className="oc-epic-card" data-testid={`epic-card-${epicID}`}>
    <Link className="oc-epic-card-goal" to={to}>{epic.data.goal || children}</Link>
    <span className={`oc-epic-card-status oc-epic-card-status--${status.tone}`}>{status.text}</span>
    <span className="oc-epic-card-id">{epicID} · {epic.data.initialProject}</span>
    {readyPlans.map((issue) => <Button key={issue.id} type="button" variant="accent" disabled={claim.isPending} onClick={() => claim.mutate(issue.id, { onSuccess: ({ session }) => navigate(`/session/${encodeURIComponent(session.id)}?factoryEpic=${encodeURIComponent(epicID)}`) })}>{claim.isPending ? 'Claiming plan…' : 'Claim plan'}</Button>)}
    {claim.isError && <span role="alert">{claim.error instanceof Error ? claim.error.message : 'Could not claim planning work.'}</span>}
  </span>;
}
