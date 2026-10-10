import { Link } from 'react-router-dom';
import { Button } from './Control';
import { useDecideFactoryControlAction, useFactoryControlAction } from '../lib/queries';

const OUTCOME = { confirmed: 'Running…', dismissed: 'Dismissed', done: 'Done', failed: 'Failed' } as const;

/** A change the control session proposed. It runs only when the user confirms it here. */
export function FactoryControlCard({ epicID, actionID }: { epicID: string; actionID: string }) {
	const action = useFactoryControlAction(epicID, actionID);
	const decide = useDecideFactoryControlAction(epicID, actionID);
	const epicLink = <Link to={`/factory/epics/${encodeURIComponent(epicID)}`}>Open epic</Link>;
	if (action.isError) return <span role="alert">Could not load the proposed change. {epicLink}</span>;
	if (!action.data) return null;
	const { status, summary, result } = action.data;
	return <div className="oc-epic-card oc-factory-action-card" aria-label="Proposed Factory change">
		<span className="oc-factory-action-issue">
			<strong>{summary}</strong>
			{status === 'pending'
				? <span className="oc-factory-action-buttons">
					<Button type="button" variant="accent" disabled={decide.isPending} onClick={() => decide.mutate('confirm')}>{decide.isPending && decide.variables === 'confirm' ? 'Confirming…' : 'Confirm'}</Button>
					<Button type="button" disabled={decide.isPending} onClick={() => decide.mutate('dismiss')}>Dismiss</Button>
				</span>
				: <span role="status">{OUTCOME[status]}{result ? `: ${result}` : ''}</span>}
			{decide.isError && <span role="alert">{decide.error instanceof Error ? decide.error.message : 'Could not decide the change.'}</span>}
		</span>
		<span className="oc-factory-action-buttons">{epicLink}</span>
	</div>;
}
