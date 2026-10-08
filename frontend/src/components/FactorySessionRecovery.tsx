import { useFactoryGraphIssues, useWorkEpics } from '../lib/queries';
import { InlineAlert } from './InlineAlert';
import { FactoryRecoveryActions } from './FactoryRecoveryActions';
import { useDocumentVisible } from '../lib/usePanelVisible';

export function FactorySessionRecovery({ platformID, sessionID }: { platformID: string; sessionID: string }) {
  const visible = useDocumentVisible();
  const epics = useWorkEpics(visible);
  const matchingEpics = epics.data?.filter((epic) => epic.attempts?.some((attempt) => attempt.session.id === sessionID && attempt.session.platform === platformID));
  const issues = useFactoryGraphIssues(matchingEpics, visible);
  if (epics.isError || issues.some((query) => query.isError)) return <InlineAlert compact retrying={epics.isFetching || issues.some((query) => query.isFetching)} onRetry={() => { void epics.refetch(); issues.forEach((query) => { void query.refetch(); }); }}>Could not load Factory recovery.</InlineAlert>;
  return <>{issues.flatMap((query) => query.data ?? []).map((issue) => {
    const gate = issue.recovery;
    if (!gate || ['resume', 'retry', 'cancel'].includes(gate.resolution)) return null;
    const epic = matchingEpics?.find((candidate) => candidate.id === gate.epicId);
    const attempt = epic?.attempts?.find((candidate) => candidate.id === gate.attemptId);
    if (attempt?.session.id !== sessionID || attempt.session.platform !== platformID) return null;
    return <aside key={gate.issueId} className="factory-plan-approval" aria-label="Factory recovery"><FactoryRecoveryActions gate={gate} /></aside>;
  })}</>;
}
