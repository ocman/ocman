import { useFactoryGraphIssues, useWorkEpics } from '../lib/queries';
import { Button } from './Control';
import { FactoryRecoveryActions } from './FactoryRecoveryActions';

export function FactorySessionRecovery({ platformID, sessionID }: { platformID: string; sessionID: string }) {
  const epics = useWorkEpics();
  const matchingEpics = epics.data?.filter((epic) => epic.attempts?.some((attempt) => attempt.session.id === sessionID && attempt.session.platform === platformID));
  const issues = useFactoryGraphIssues(matchingEpics);
  if (epics.isError || issues.some((query) => query.isError)) return <aside className="factory-plan-approval" role="alert">Could not load Factory recovery.<Button type="button" onClick={() => { void epics.refetch(); issues.forEach((query) => { void query.refetch(); }); }}>Retry</Button></aside>;
  return <>{issues.flatMap((query) => query.data ?? []).map((issue) => {
    const gate = issue.recovery;
    if (!gate || ['resume', 'retry', 'cancel'].includes(gate.resolution)) return null;
    const epic = matchingEpics?.find((candidate) => candidate.id === gate.epicId);
    const attempt = epic?.attempts?.find((candidate) => candidate.id === gate.attemptId);
    if (attempt?.session.id !== sessionID || attempt.session.platform !== platformID) return null;
    return <aside key={gate.issueId} className="factory-plan-approval" aria-label="Factory recovery"><FactoryRecoveryActions gate={gate} /></aside>;
  })}</>;
}
