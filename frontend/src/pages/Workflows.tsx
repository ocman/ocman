import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type WorkflowArtifact, type WorkflowDefinition, type WorkflowMapItemRun, type WorkflowRun, type WorkflowRunDetail, type WorkflowValidation, type WorkflowVersion } from '../lib/api';
import { useWorkflows } from '../lib/useCapabilities';
import { onSseConnect, onWorkflowRunUpdated, onWorkflowTriggerUpdated } from '../lib/useGlobalEvents';
import { usePageTitle } from '../lib/headerContext';
import './Workflows.css';

const EXAMPLE = `id: release
name: Release approvals
version: "1"
concurrency: 1
triggers:
  - id: manual
    type: manual
nodes:
  - id: review
    name: Review
    type: approval
  - id: ship
    name: Ship
    type: approval
dependencies:
  - from: review
    to: ship
`;

export function Workflows() {
	usePageTitle('Workflows');
	const enabled = useWorkflows();
	const [source, setSource] = useState(EXAMPLE);
	const [versions, setVersions] = useState<WorkflowVersion[]>([]);
	const [runs, setRuns] = useState<WorkflowRun[]>([]);
	const [selected, setSelected] = useState<WorkflowRunDetail>();
	const selectedID = useRef<string | undefined>(undefined);
	const validationID = useRef(0);
	const [error, setError] = useState('');
	const [validated, setValidated] = useState<WorkflowValidation>();
	const [compareFrom, setCompareFrom] = useState('');
	const [compareTo, setCompareTo] = useState('');

	function select(run: WorkflowRunDetail) {
		selectedID.current = run.id;
		setSelected(run);
	}

	async function refresh() {
		const [nextVersions, nextRuns] = await Promise.all([api.workflows.versions(), api.workflows.runs()]);
		setVersions(nextVersions);
		setRuns(nextRuns);
		setCompareFrom((current) => current || nextVersions[1]?.id || nextVersions[0]?.id || '');
		setCompareTo((current) => current || nextVersions[0]?.id || '');
		const id = selectedID.current ?? nextRuns[0]?.id;
		if (id) select(await api.workflows.run(id));
	}

	useEffect(() => {
		if (!enabled) return;
		void refresh().catch((reason) => setError(String(reason)));
		void validate();
		const unsubscribeRun = onWorkflowRunUpdated(() => {
			void refresh().catch((reason) => setError(String(reason)));
		});
		const unsubscribeTrigger = onWorkflowTriggerUpdated(() => {
			void refresh().catch((reason) => setError(String(reason)));
		});
		const unsubscribeConnect = onSseConnect(() => {
			void refresh().catch((reason) => setError(String(reason)));
		});
		return () => { unsubscribeRun(); unsubscribeTrigger(); unsubscribeConnect(); };
	// Selection lives in a ref so reconnects refresh it without resubscribing.
	// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [enabled]);

	async function validate() {
		const requestID = ++validationID.current;
		setError('');
		try {
			const result = await api.workflows.validate(source);
			if (requestID === validationID.current) setValidated(result);
		} catch (reason) {
			if (requestID !== validationID.current) return;
			setValidated(undefined);
			setError(reason instanceof Error ? reason.message : String(reason));
		}
	}

	function editSource(value: string) {
		validationID.current++;
		setSource(value);
		setValidated(undefined);
		setError('');
	}

	async function publish() {
		setError('');
		try {
			await api.workflows.publish(source);
			await refresh();
		} catch (reason) {
			setError(reason instanceof Error ? reason.message : String(reason));
		}
	}

	async function activate(id: string) {
		setError('');
		try {
			await api.workflows.activate(id);
			await refresh();
		} catch (reason) {
			setError(reason instanceof Error ? reason.message : String(reason));
		}
	}

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
				<div><span className="workflow-kicker">Immutable YAML or JSON definitions</span><h1>Workflow versions</h1><p>Validate a definition, inspect its graph, then publish and activate an immutable version.</p></div>
				<label>Workflow YAML or JSON<textarea aria-label="Workflow YAML or JSON" value={source} onChange={(event) => editSource(event.target.value)} spellCheck={false} /></label>
				<div className="workflow-controls"><button type="button" onClick={() => void validate()}>Validate</button><button type="button" onClick={() => void publish()}>Publish version</button></div>
				{error && <p role="alert">{error}</p>}
				{validated && <DefinitionGraph definition={validated.definition} />}
			</section>

			<section className="workflow-history" aria-label="Workflow history">
				<div><h2>Published versions</h2>{versions.map((version) => <article key={version.id}><strong>{version.name}</strong> <small>rev {version.revision} · {version.definition.version}{version.active ? ' · active' : ''}</small><div className="workflow-controls"><button type="button" onClick={() => void activate(version.id)}>Activate revision {version.revision}</button><a href={api.workflows.exportUrl(version.id)} download={`${version.workflowId}-v${version.revision}.yaml`}>Export revision {version.revision}</a>{version.active && <button type="button" onClick={() => void mutate(() => api.workflows.startActive(version.workflowId))}>Start active {version.workflowId}</button>}</div>{version.triggerStates.map((trigger) => <div className="workflow-trigger" key={trigger.id}><strong>{trigger.id} · {trigger.type} · {trigger.overlap ?? 'skip'}</strong><small>Next {formatTime(trigger.nextCheckAt)} · Last {formatTime(trigger.lastFiredAt)} ({trigger.lastDecision ?? 'never'}) · {trigger.queued} queued{trigger.lastRunId ? ` · run ${trigger.lastRunId}` : ''}</small></div>)}</article>)}</div>
				<div><h2>Runs</h2>{runs.map((run) => <button type="button" aria-pressed={selected?.id === run.id} key={run.id} onClick={() => void api.workflows.run(run.id).then(select)}>{run.workflowId} <small>{run.state}</small></button>)}</div>
			</section>
			<VersionComparison versions={versions} from={compareFrom} to={compareTo} onFrom={setCompareFrom} onTo={setCompareTo} />

			{selected && <RunView run={selected} mutate={mutate} onSelectRun={(id) => void api.workflows.run(id).then(select).catch((reason) => setError(String(reason)))} />}
		</main>
	);
}

function RunView({ run, mutate, onSelectRun }: { run: WorkflowRunDetail; mutate: (action: () => Promise<WorkflowRunDetail>) => Promise<void>; onSelectRun: (id: string) => void }) {
	const [artifacts, setArtifacts] = useState<WorkflowArtifact[]>([]);
	useEffect(() => {
		let active = true;
		void api.workflows.artifacts(run.id).then((next) => { if (active) setArtifacts(next); }).catch(() => { if (active) setArtifacts([]); });
		return () => { active = false; };
	}, [run.id, run.state, run.updatedAt]);
	return (
		<section className="workflow-run" aria-label="Workflow run">
			<header><div><span className="workflow-kicker">{run.id}</span><h2>{run.version.name}</h2><p>Revision {run.version.revision} · definition {run.version.definition.version} · {run.state}</p></div><div className="workflow-controls">{run.state === 'active' && <button type="button" onClick={() => void mutate(() => api.workflows.pause(run.id))}>Pause run</button>}{(run.state === 'active' || run.state === 'paused') && <button type="button" onClick={() => void mutate(() => api.workflows.cancel(run.id))}>Cancel run</button>}</div></header>
			{run.parentRunId && <p className="workflow-run-parent">Mapped item <strong>{run.itemKey}</strong> of <button type="button" onClick={() => onSelectRun(run.parentRunId!)}>parent run {run.parentNodeId}</button></p>}
			{run.trigger && <p className="workflow-run-trigger"><strong>{run.trigger.id} · {run.trigger.type} · {run.trigger.overlap ?? 'skip'}</strong> · {run.trigger.detail} · fired {formatTime(run.trigger.firedAt)}</p>}
			{run.resources && run.resources.length > 0 && <ul className="workflow-resources" aria-label="Resource pools">{run.resources.map((pool) => <li key={pool.pool || 'run'}><strong>{pool.pool || 'run concurrency'}</strong>: {pool.held}/{pool.capacity} held{pool.waiting && pool.waiting.length > 0 && <span> · waiting: {pool.waiting.join(', ')}</span>}</li>)}</ul>}
			{run.workspace && run.workspace.length > 0 && <ul className="workflow-leases" aria-label="Workspace leases">{run.workspace.map((lease) => <li key={lease.nodeId}><strong>{lease.nodeId}</strong>: shard {lease.shard} · {lease.mode}{lease.commit && ' (commit)'}{lease.paths && lease.paths.length > 0 && <span> · {lease.paths.join(', ')}</span>}{lease.host && <span> · host {lease.host}</span>}</li>)}</ul>}
			<div className="workflow-graph" role="region" aria-label="Workflow run graph">
				{run.nodes.map((node, index) => (
					<div className="workflow-step" key={node.nodeId}>
						{index > 0 && <span aria-hidden="true">-&gt;</span>}
						<RunNode run={run} node={node} mutate={mutate} onSelectRun={onSelectRun} />
					</div>
				))}
			</div>
			<ArtifactList runId={run.id} artifacts={artifacts} />
		</section>
	);
}

function DefinitionGraph({ definition }: { definition: WorkflowDefinition }) {
	return <div className="workflow-graph" role="region" aria-label="Workflow definition graph">{definition.nodes.map((node) => <article key={node.id}><small>{node.type} · {node.id}</small><h3>{node.name}</h3>{node.subworkflow && <p>Uses {node.subworkflow.workflowId}</p>}<p>{definition.dependencies.filter((dependency) => dependency.to === node.id).map((dependency) => dependency.from).join(', ') || 'Entry node'}</p></article>)}</div>;
}

function VersionComparison({ versions, from, to, onFrom, onTo }: { versions: WorkflowVersion[]; from: string; to: string; onFrom: (id: string) => void; onTo: (id: string) => void }) {
	if (!versions.length) return null;
	const left = versions.find((version) => version.id === from) ?? versions[0];
	const right = versions.find((version) => version.id === to) ?? versions[0];
	return <section className="workflow-run" role="region" aria-label="Version comparison"><h2>Compare versions</h2><div className="workflow-controls"><label>Compare from<select aria-label="Compare from" value={left.id} onChange={(event) => onFrom(event.target.value)}>{versions.map((version) => <option key={version.id} value={version.id}>Revision {version.revision}</option>)}</select></label><label>Compare to<select aria-label="Compare to" value={right.id} onChange={(event) => onTo(event.target.value)}>{versions.map((version) => <option key={version.id} value={version.id}>Revision {version.revision}</option>)}</select></label></div><div className="workflow-history"><pre>Revision {left.revision}{'\n'}{JSON.stringify(left.definition, null, 2)}</pre><pre>Revision {right.revision}{'\n'}{JSON.stringify(right.definition, null, 2)}</pre></div></section>;
}

function RunNode({ run, node, mutate, onSelectRun }: { run: WorkflowRunDetail; node: WorkflowRunDetail['nodes'][number]; mutate: (action: () => Promise<WorkflowRunDetail>) => Promise<void>; onSelectRun: (id: string) => void }) {
	const attempt = node.attempts.at(-1);
	const conditions = run.version.definition.dependencies.filter((edge) => edge.to === node.nodeId && edge.condition).map((edge) => edge.condition!);
	const attemptLine = attempt
		? `Attempt ${attempt.seq}: ${attempt.state}${attempt.exitCode !== undefined && attempt.exitCode >= 0 ? ` (exit ${attempt.exitCode})` : ''}`
		: 'Not attempted';
	return (
		<article data-state={node.state}>
			<small>{node.type} · {node.nodeId}</small>
			<h3>{node.name}</h3>
			<strong>{node.state}</strong>
			<p>{attemptLine}</p>
			{conditions.map((condition) => <p key={condition} className="workflow-condition">Condition {node.state === 'skipped' ? 'skipped' : 'evaluated'}: <code>{condition}</code></p>)}
			{node.attempts.length > 1 && <p className="workflow-repeat-history">Repeat history: {node.attempts.map((item) => `#${item.seq} ${item.state}`).join(', ')}</p>}
			{attempt?.resolvedAt && <p>Resolved by {attempt.resolvedBy || 'user'} · {formatTime(attempt.resolvedAt)}</p>}
			{node.type === 'agent' && <AgentNode attempt={attempt} />}
			{attempt?.state === 'unknown' && run.state === 'paused' && <div className="workflow-controls"><button type="button" onClick={() => void mutate(() => api.workflows.resolveUnknown(run.id, attempt.id, 'successful'))}>Mark succeeded</button><button type="button" onClick={() => void mutate(() => api.workflows.resolveUnknown(run.id, attempt.id, 'failed'))}>Mark failed</button><button type="button" onClick={() => void mutate(() => api.workflows.resolveUnknown(run.id, attempt.id, 'retry'))}>Retry safely</button></div>}
			{node.type === 'approval' && node.state === 'ready' && run.state === 'active' && <button type="button" onClick={() => void mutate(() => api.workflows.approve(run.id, node.nodeId))}>Approve {node.name}</button>}
			{attempt && node.type === 'command' && <CommandAttempt attempt={attempt} />}
			{node.type === 'map' && <MapNode items={(run.children ?? []).filter((item) => item.mapNode === node.nodeId)} error={node.attempts.map((a) => a.error).find(Boolean)} onSelectRun={onSelectRun} />}
			{node.type === 'join' && <JoinNode attempt={attempt} />}
		</article>
	);
}

function AgentNode({ attempt }: { attempt?: WorkflowRunDetail['nodes'][number]['attempts'][number] }) {
	if (!attempt) return null;
	return (
		<>
			{attempt.sessionId && <><Link to={`/session/${encodeURIComponent(attempt.sessionId)}`}>Open agent session</Link><p>Session: {attempt.sessionState}</p></>}
			{attempt.outputs && Object.entries(attempt.outputs).map(([name, value]) => <p key={name}>{name}: {JSON.stringify(value)}</p>)}
			{attempt.error && <p role="alert">{attempt.error}</p>}
		</>
	);
}

function MapNode({ items, error, onSelectRun }: { items: WorkflowMapItemRun[]; error?: string; onSelectRun: (id: string) => void }) {
	const [expanded, setExpanded] = useState(false);
	const counts = items.reduce<Record<string, number>>((acc, item) => { acc[item.state] = (acc[item.state] ?? 0) + 1; return acc; }, {});
	const summary = Object.entries(counts).map(([state, count]) => `${count} ${state}`).join(', ') || 'no items';
	return (
		<div className="workflow-map">
			{error && <p role="alert">{error}</p>}
			<button type="button" className="workflow-map-toggle" aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}>
				{expanded ? 'Collapse' : 'Expand'} {items.length} mapped item{items.length === 1 ? '' : 's'} <small>({summary})</small>
			</button>
			{expanded && (
				<ul className="workflow-map-items" data-testid="workflow-map-items">
					{items.map((item) => (
						<li key={item.key} data-state={item.state} data-testid="workflow-map-item">
							<strong>{item.key}</strong> <small>#{item.index} · {item.state}</small>
							{item.childRunId && <button type="button" onClick={() => onSelectRun(item.childRunId!)}>Open item run</button>}
						</li>
					))}
				</ul>
			)}
		</div>
	);
}

function JoinNode({ attempt }: { attempt?: WorkflowRunDetail['nodes'][number]['attempts'][number] }) {
	const raw = attempt?.outputs?.result;
	if (!raw || typeof raw !== 'object') return null;
	const result = raw as { policy?: string; success?: number; failed?: number; total?: number; items?: { key: string; state: string; index: number }[] };
	return (
		<div className="workflow-join" data-testid="workflow-join">
			<p><strong>{result.policy}</strong> · {result.success ?? 0}/{result.total ?? 0} succeeded</p>
			<ol className="workflow-join-items">
				{(result.items ?? []).map((item) => <li key={item.key} data-state={item.state}>{item.key}: {item.state}</li>)}
			</ol>
		</div>
	);
}

function ArtifactList({ runId, artifacts }: { runId: string; artifacts: WorkflowArtifact[] }) {
	if (artifacts.length === 0) return null;
	return (
		<section className="workflow-artifacts" aria-label="Workflow artifacts">
			<h3>Artifacts</h3>
			<ul>
				{artifacts.map((artifact) => (
					<li key={artifact.id} data-testid="workflow-artifact">
						<strong>{artifact.name}</strong> <small>{artifact.kind} · {formatSize(artifact.size)}{artifact.expiresAt ? ` · expires ${formatTime(artifact.expiresAt)}` : ' · retained'}</small>
						{artifact.payloadAvailable
							? <a href={api.workflows.artifactDownloadUrl(runId, artifact.id)} download>Download</a>
							: <span className="workflow-artifact-gone">Payload cleaned up</span>}
					</li>
				))}
			</ul>
		</section>
	);
}

function formatSize(bytes: number) {
	if (bytes < 1024) return `${bytes} B`;
	if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
	return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function CommandAttempt({ attempt }: { attempt: WorkflowRunDetail['nodes'][number]['attempts'][number] }) {
	return <div className="workflow-command-log">
		{attempt.error && <p><b>Error</b>: {attempt.error}</p>}
		{attempt.stdout && <pre aria-label="stdout">{attempt.stdout}{attempt.stdoutTruncated && '\n[truncated]'}</pre>}
		{attempt.stderr && <pre aria-label="stderr">{attempt.stderr}{attempt.stderrTruncated && '\n[truncated]'}</pre>}
		{Object.entries(attempt.outputs ?? {}).map(([name, value]) => <div key={name}><b>{name}</b><pre>{String(value)}</pre></div>)}
	</div>;
}

function formatTime(value?: number) {
	return value ? new Date(value).toLocaleString() : 'not scheduled';
}
