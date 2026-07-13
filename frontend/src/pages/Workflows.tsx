import { useEffect, useRef, useState } from 'react';
import { api, type WorkflowRun, type WorkflowRunDetail, type WorkflowVersion } from '../lib/api';
import { useWorkflows } from '../lib/useCapabilities';
import { onSseConnect, onWorkflowRunUpdated } from '../lib/useGlobalEvents';
import { usePageTitle } from '../lib/headerContext';
import './Workflows.css';

const EXAMPLE = `{
  "id": "release",
  "name": "Release approvals",
  "version": "1",
  "concurrency": 1,
  "nodes": [
    {"id": "review", "name": "Review", "type": "approval"},
    {"id": "ship", "name": "Ship", "type": "approval"}
  ],
  "dependencies": [{"from": "review", "to": "ship"}]
}`;

export function Workflows() {
	usePageTitle('Workflows');
	const enabled = useWorkflows();
	const [source, setSource] = useState(EXAMPLE);
	const [versions, setVersions] = useState<WorkflowVersion[]>([]);
	const [runs, setRuns] = useState<WorkflowRun[]>([]);
	const [selected, setSelected] = useState<WorkflowRunDetail>();
	const selectedID = useRef<string | undefined>(undefined);
	const [error, setError] = useState('');

	function select(run: WorkflowRunDetail) {
		selectedID.current = run.id;
		setSelected(run);
	}

	async function refresh() {
		const [nextVersions, nextRuns] = await Promise.all([api.workflows.versions(), api.workflows.runs()]);
		setVersions(nextVersions);
		setRuns(nextRuns);
		const id = selectedID.current ?? nextRuns[0]?.id;
		if (id) select(await api.workflows.run(id));
	}

	useEffect(() => {
		if (!enabled) return;
		void refresh().catch((reason) => setError(String(reason)));
		const unsubscribeRun = onWorkflowRunUpdated(() => {
			void refresh().catch((reason) => setError(String(reason)));
		});
		const unsubscribeConnect = onSseConnect(() => {
			void refresh().catch((reason) => setError(String(reason)));
		});
		return () => { unsubscribeRun(); unsubscribeConnect(); };
	// Selection lives in a ref so reconnects refresh it without resubscribing.
	// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [enabled]);

	async function mutate(action: () => Promise<WorkflowRunDetail>) {
		setError('');
		try {
			const run = await action();
			select(run);
			await refresh();
		} catch (reason) {
			setError(reason instanceof Error ? reason.message : String(reason));
		}
	}

	if (!enabled) return <main className="workflow-page"><p>Workflows are unavailable on this host.</p></main>;

	return (
		<main className="workflow-page" data-testid="workflows-page">
			<section className="workflow-author" aria-label="Workflow authoring">
				<div><span className="workflow-kicker">Immutable JSON definitions</span><h1>Approval workflows</h1><p>Publish a version, start a run, then approve each ready gate in dependency order.</p></div>
				<label>Workflow JSON<textarea aria-label="Workflow JSON" value={source} onChange={(event) => setSource(event.target.value)} spellCheck={false} /></label>
				<button type="button" onClick={() => void mutate(async () => { const version = await api.workflows.publish(source); return api.workflows.start(version.id); })}>Publish and start</button>
				{error && <p role="alert">{error}</p>}
			</section>

			<section className="workflow-history" aria-label="Workflow history">
				<div><h2>Published versions</h2>{versions.map((version) => <button type="button" key={version.id} onClick={() => void mutate(() => api.workflows.start(version.id))}>{version.name} <small>rev {version.revision} · {version.definition.version}</small></button>)}</div>
				<div><h2>Runs</h2>{runs.map((run) => <button type="button" aria-pressed={selected?.id === run.id} key={run.id} onClick={() => void api.workflows.run(run.id).then(select)}>{run.workflowId} <small>{run.state}</small></button>)}</div>
			</section>

			{selected && <RunView run={selected} mutate={mutate} />}
		</main>
	);
}

function RunView({ run, mutate }: { run: WorkflowRunDetail; mutate: (action: () => Promise<WorkflowRunDetail>) => Promise<void> }) {
	return (
		<section className="workflow-run" aria-label="Workflow run">
			<header><div><span className="workflow-kicker">{run.id}</span><h2>{run.version.name}</h2><p>Revision {run.version.revision} · definition {run.version.definition.version} · {run.state}</p></div><div className="workflow-controls">{run.state === 'active' && <button type="button" onClick={() => void mutate(() => api.workflows.pause(run.id))}>Pause run</button>}{(run.state === 'active' || run.state === 'paused') && <button type="button" onClick={() => void mutate(() => api.workflows.cancel(run.id))}>Cancel run</button>}</div></header>
			<div className="workflow-graph" role="region" aria-label="Workflow run graph">
				{run.nodes.map((node, index) => <div className="workflow-step" key={node.nodeId}>{index > 0 && <span aria-hidden="true">-&gt;</span>}<article data-state={node.state}><small>approval · {node.nodeId}</small><h3>{node.name}</h3><strong>{node.state}</strong><p>{node.attempts.length ? `Attempt ${node.attempts[0].seq}: ${node.attempts[0].state}` : 'Not attempted'}</p>{node.state === 'ready' && run.state === 'active' && <button type="button" onClick={() => void mutate(() => api.workflows.approve(run.id, node.nodeId))}>Approve {node.name}</button>}</article></div>)}
			</div>
		</section>
	);
}
