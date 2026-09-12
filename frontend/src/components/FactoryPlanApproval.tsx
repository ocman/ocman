import { useState } from 'react';
import { Button } from './Control';
import { FactoryStartedToast } from './FactoryStartedToast';
import { useDecideFactoryPlanGate, useFactoryIssues, useWorkEpic } from '../lib/queries';

export function FactoryPlanApproval({ epicID, platformID, sessionID }: { epicID: string; platformID: string; sessionID: string }) {
	const epic = useWorkEpic(epicID);
	const issues = useFactoryIssues(epicID);
	const decide = useDecideFactoryPlanGate(epicID);
	const [started, setStarted] = useState(false);
	const gate = epic.data?.planGate;
	if (epic.isError || issues.isError) return <aside className="factory-plan-approval" role="alert">Could not load plan approval.<Button type="button" onClick={() => void Promise.all([epic.refetch(), issues.refetch()])}>Retry</Button></aside>;
	const planningAttempt = epic.data?.attempts?.at(-1);
	const matchingSession = planningAttempt?.session.id === sessionID && planningAttempt.session.platform === platformID;
	const materialization = issues.data?.find((issue) => issue.kind === 'materialization' && issue.status === 'open');
	const visible = matchingSession && materialization && (gate?.resolution === 'open' || gate?.resolution === 'approved');
	const approve = async () => {
		try {
			if (!visible) return;
			await decide.mutateAsync({ action: 'approve', expectedRevision: gate.proposalRevision, expectedHash: gate.proposalHash });
			setStarted(true);
		} catch { /* Mutation errors are rendered below. */ }
	};
	const error = decide.error;
	return <><FactoryStartedToast open={started} onOpenChange={setStarted} />{visible && <aside className="factory-plan-approval" aria-label="Plan approval">
		<span><strong>{gate.resolution === 'open' ? 'Plan ready for approval.' : 'Plan approved.'}</strong> Approval starts implementation.</span>
		<Button type="button" variant="accent" disabled={decide.isPending} onClick={() => void approve()}>{decide.isPending ? 'Starting…' : gate.resolution === 'open' ? 'Approve plan' : 'Retry starting work'}</Button>
		{error && <span role="alert">{error instanceof Error ? error.message : 'Could not start implementation.'}</span>}
	</aside>}</>;
}
