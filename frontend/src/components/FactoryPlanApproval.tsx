import { Button } from './Control';
import { useDecideFactoryPlanGate, useFactoryIssues, useMaterializeFactoryPlan, useWorkEpic } from '../lib/queries';

export function FactoryPlanApproval({ epicID, platformID, sessionID }: { epicID: string; platformID: string; sessionID: string }) {
	const epic = useWorkEpic(epicID);
	const issues = useFactoryIssues(epicID);
	const decide = useDecideFactoryPlanGate(epicID);
	const materialize = useMaterializeFactoryPlan();
	const gate = epic.data?.planGate;
	if (!epicID) return null;
	if (epic.isError || issues.isError) return <aside className="factory-plan-approval" role="alert">Could not load plan approval.<Button type="button" onClick={() => void Promise.all([epic.refetch(), issues.refetch()])}>Retry</Button></aside>;
	const planningAttempt = epic.data?.attempts?.at(-1);
	const matchingSession = planningAttempt?.session.id === sessionID && planningAttempt.session.platform === platformID;
	const materialization = issues.data?.find((issue) => issue.kind === 'materialization' && issue.status === 'open');
	if (!matchingSession || !materialization || (gate?.resolution !== 'open' && gate?.resolution !== 'approved')) return null;
	const approve = async () => {
		try {
			if (gate.resolution === 'open') await decide.mutateAsync({ action: 'approve', expectedRevision: gate.proposalRevision, expectedHash: gate.proposalHash });
			await materialize.mutateAsync({ epicId: epicID, issueId: materialization.id });
		} catch { /* Mutation errors are rendered below. */ }
	};
	const error = decide.error ?? materialize.error;
	return <aside className="factory-plan-approval" aria-label="Plan approval">
		<span><strong>{gate.resolution === 'open' ? 'Plan ready for approval.' : 'Plan approved.'}</strong> Approval materializes the proposed Issues and starts implementation.</span>
		<Button type="button" variant="accent" disabled={decide.isPending || materialize.isPending} onClick={() => void approve()}>{decide.isPending || materialize.isPending ? 'Starting…' : gate.resolution === 'open' ? 'Approve and start implementation' : 'Start implementation'}</Button>
		{error && <span role="alert">{error instanceof Error ? error.message : 'Could not start implementation.'}</span>}
	</aside>;
}
