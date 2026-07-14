// @vitest-environment jsdom
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkflowRunDetail, WorkflowVersion } from '../lib/api';

const { apiMock, useWorkflowsMock, listeners, triggerListeners, connectListeners } = vi.hoisted(() => ({
	apiMock: {
		versions: vi.fn(), validate: vi.fn(), publish: vi.fn(), activate: vi.fn(), startActive: vi.fn(), start: vi.fn(), runs: vi.fn(), run: vi.fn(), approve: vi.fn(), pause: vi.fn(), cancel: vi.fn(), resolveUnknown: vi.fn(),
		artifacts: vi.fn(), artifactDownloadUrl: vi.fn((runId: string, id: string) => `/api/workflow-runs/${runId}/artifacts/${id}/download`),
		exportUrl: vi.fn(),
	},
	useWorkflowsMock: vi.fn(() => true),
	listeners: [] as Array<(runId: string) => void>,
	triggerListeners: [] as Array<() => void>,
	connectListeners: [] as Array<() => void>,
}));

vi.mock('../lib/api', () => ({ api: { workflows: apiMock } }));
vi.mock('../lib/useCapabilities', () => ({ useWorkflows: useWorkflowsMock }));
vi.mock('../lib/useGlobalEvents', () => ({
	onWorkflowRunUpdated: (listener: (runId: string) => void) => {
		listeners.push(listener);
		return () => {};
	},
	onWorkflowTriggerUpdated: (listener: () => void) => {
		triggerListeners.push(listener);
		return () => {};
	},
	onSseConnect: (listener: () => void) => {
		connectListeners.push(listener);
		return () => {};
	},
}));

import { Workflows } from './Workflows';

const version: WorkflowVersion = {
	id: 'wfv_1', workflowId: 'release', name: 'Release approvals', revision: 1, createdAt: 1, active: true,
	definition: {
		id: 'release', name: 'Release approvals', version: '1', concurrency: 1,
		triggers: [{ id: 'manual', type: 'manual' }, { id: 'timer', type: 'interval', intervalSeconds: 60, overlap: 'queue' }],
		nodes: [{ id: 'review', name: 'Review', type: 'approval' }, { id: 'ship', name: 'Ship', type: 'approval' }],
		dependencies: [{ from: 'review', to: 'ship' }],
	},
	triggerStates: [{ id: 'manual', type: 'manual', overlap: 'skip', versionId: 'wfv_1', queued: 0 }, { id: 'timer', type: 'interval', intervalSeconds: 60, overlap: 'queue', versionId: 'wfv_1', nextCheckAt: 120000, lastFiredAt: 60000, lastDecision: 'queued', lastRunId: 'wfr_1', queued: 1 }],
};

const activeRun: WorkflowRunDetail = {
	id: 'wfr_1', workflowId: 'release', versionId: version.id, state: 'active', createdAt: 1, updatedAt: 1, version,
	trigger: { id: 'timer', type: 'interval', intervalSeconds: 60, overlap: 'queue', versionId: 'wfv_1', firedAt: 60000, detail: 'scheduled (every 1m0s)' },
	nodes: [
		{ nodeId: 'review', name: 'Review', type: 'approval', state: 'ready', attempts: [{ id: 1, seq: 1, state: 'waiting', startedAt: 1 }] },
		{ nodeId: 'ship', name: 'Ship', type: 'approval', state: 'pending', attempts: [] },
	],
};

describe('Workflows', () => {
	beforeEach(() => {
		vi.clearAllMocks();
		listeners.length = 0;
		triggerListeners.length = 0;
		connectListeners.length = 0;
		useWorkflowsMock.mockReturnValue(true);
		apiMock.versions.mockResolvedValue([version]);
		apiMock.runs.mockResolvedValue([activeRun]);
		apiMock.run.mockResolvedValue(activeRun);
		apiMock.publish.mockResolvedValue(version);
		apiMock.validate.mockResolvedValue({ definition: version.definition, canonicalJson: version.definition, yaml: 'id: release\n' });
		apiMock.activate.mockResolvedValue(version);
		apiMock.startActive.mockResolvedValue(activeRun);
		apiMock.exportUrl.mockReturnValue('/api/workflows/wfv_1/export');
		apiMock.start.mockResolvedValue(activeRun);
		apiMock.approve.mockResolvedValue({ ...activeRun, nodes: [{ ...activeRun.nodes[0], state: 'successful' }, { ...activeRun.nodes[1], state: 'ready' }] });
		apiMock.resolveUnknown.mockResolvedValue(activeRun);
		apiMock.artifacts.mockResolvedValue([]);
	});

	it('is capability gated', () => {
		useWorkflowsMock.mockReturnValue(false);
		render(<MemoryRouter><Workflows /></MemoryRouter>);
		expect(screen.getByText('Workflows are unavailable on this host.')).toBeInTheDocument();
		expect(apiMock.versions).not.toHaveBeenCalled();
	});

	it('validates YAML into the read-only graph and reports source errors', async () => {
		const user = userEvent.setup();
		render(<Workflows />);

		expect(await screen.findByRole('region', { name: 'Workflow definition graph' })).toHaveTextContent('Review');
		expect(screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Workflow YAML or JSON' }).value).toContain('id: release');
		apiMock.validate.mockRejectedValueOnce(new Error('line 2, column 1: duplicate key "name"'));
		await user.clear(screen.getByRole('textbox', { name: 'Workflow YAML or JSON' }));
		await user.type(screen.getByRole('textbox', { name: 'Workflow YAML or JSON' }), 'name: one\nname: two');
		await user.click(screen.getByRole('button', { name: 'Validate' }));
		expect(await screen.findByRole('alert')).toHaveTextContent('line 2, column 1');
	});

	it('does not show a stale graph when source changes during validation', async () => {
		const user = userEvent.setup();
		let resolve!: (value: unknown) => void;
		apiMock.validate.mockReturnValueOnce(new Promise((done) => { resolve = done; }));
		render(<Workflows />);
		await user.clear(screen.getByRole('textbox', { name: 'Workflow YAML or JSON' }));
		await act(async () => resolve({ definition: version.definition, canonicalJson: version.definition, yaml: 'id: release\n' }));
		expect(screen.queryByRole('region', { name: 'Workflow definition graph' })).not.toBeInTheDocument();
	});

	it('publishes, activates, compares, starts active, approves, and refreshes from SSE', async () => {
		const user = userEvent.setup();
		render(<MemoryRouter><Workflows /></MemoryRouter>);

		await screen.findByRole('region', { name: 'Workflow run graph' });
		expect(screen.getByText('Attempt 1: waiting')).toBeInTheDocument();
		expect(screen.getAllByText(/timer · interval · queue/)).toHaveLength(2);
		expect(screen.getByText(/Last .* \(queued\) · 1 queued/)).toBeInTheDocument();
		expect(screen.getByText(/scheduled \(every 1m0s\)/)).toBeInTheDocument();
		await user.click(screen.getByRole('button', { name: 'Approve Review' }));
		expect(apiMock.approve).toHaveBeenCalledWith('wfr_1', 'review');

		await user.click(screen.getByRole('button', { name: 'Publish version' }));
		expect(apiMock.publish).toHaveBeenCalledWith(expect.stringContaining('concurrency: 1'));
		expect(apiMock.start).not.toHaveBeenCalled();
		await user.click(screen.getByRole('button', { name: 'Activate revision 1' }));
		expect(apiMock.activate).toHaveBeenCalledWith('wfv_1');
		await user.click(screen.getByRole('button', { name: 'Start active release' }));
		expect(apiMock.startActive).toHaveBeenCalledWith('release');
		expect(screen.getByRole('link', { name: 'Export revision 1' })).toHaveAttribute('href', '/api/workflows/wfv_1/export');
		expect(screen.getByRole('region', { name: 'Version comparison' })).toHaveTextContent('Revision 1');

		listeners[0]('another-run');
		await waitFor(() => expect(apiMock.run).toHaveBeenLastCalledWith('wfr_1'));
		triggerListeners[0]();
		await waitFor(() => expect(apiMock.versions.mock.calls.length).toBeGreaterThan(3));
		connectListeners[0]();
		await waitFor(() => expect(apiMock.versions.mock.calls.length).toBeGreaterThanOrEqual(6));
	});

	it('publishes automated-only versions without trying a manual start', async () => {
		const user = userEvent.setup();
		apiMock.publish.mockResolvedValue({ ...version, definition: { ...version.definition, triggers: [{ id: 'timer', type: 'interval', intervalSeconds: 60 }] } });
		render(<Workflows />);
		await screen.findByRole('region', { name: 'Workflow run graph' });
		await user.click(screen.getByRole('button', { name: 'Publish version' }));
		expect(apiMock.publish).toHaveBeenCalled();
		expect(apiMock.start).not.toHaveBeenCalled();
	});
	it('shows command outcomes, logs, and collected outputs', async () => {
		const commandRun: WorkflowRunDetail = {
			...activeRun,
			state: 'failed',
			nodes: [{
				nodeId: 'test', name: 'Run tests', type: 'command', state: 'failed',
				attempts: [{
					id: 2, seq: 1, state: 'failed', startedAt: 1, completedAt: 2, exitCode: 7,
					stdout: 'test output', stderr: 'test failure', error: 'exit status 7', outputs: { report: '{"failed":1}' },
				}],
			}],
		};
		apiMock.runs.mockResolvedValue([commandRun]);
		apiMock.run.mockResolvedValue(commandRun);
		render(<MemoryRouter><Workflows /></MemoryRouter>);

		expect(await screen.findByText(/failed \(exit 7\)/)).toBeInTheDocument();
		expect(screen.getByText('test output')).toBeInTheDocument();
		expect(screen.getByText('test failure')).toBeInTheDocument();
		expect(screen.getByText('{"failed":1}')).toBeInTheDocument();
	});

	it('shows workspace shard leases and ownership', async () => {
		const leasedRun: WorkflowRunDetail = {
			...activeRun,
			workspace: [
				{ nodeId: 'edit-a', attemptId: 1, shard: 0, mode: 'path', paths: ['src/a'], host: 'local-host', acquiredAt: 1 },
				{ nodeId: 'commit', attemptId: 2, shard: 0, mode: 'exclusive', commit: true, acquiredAt: 2 },
			],
		};
		apiMock.runs.mockResolvedValue([leasedRun]);
		apiMock.run.mockResolvedValue(leasedRun);
		render(<MemoryRouter><Workflows /></MemoryRouter>);

		await screen.findByRole('list', { name: 'Workspace leases' });
		expect(screen.getByText(/shard 0 · path/)).toBeInTheDocument();
		expect(screen.getByText(/src\/a/)).toBeInTheDocument();
		expect(screen.getByText(/host local-host/)).toBeInTheDocument();
		expect(screen.getByText(/shard 0 · exclusive \(commit\)/)).toBeInTheDocument();
	});

	it('inspects artifact metadata and offers retained-payload download', async () => {
		apiMock.artifacts.mockResolvedValue([
			{ id: 'wfa_1', runId: 'wfr_1', nodeId: 'review', attemptId: 1, name: 'report', kind: 'json', contentHash: 'abc', size: 2048, createdAt: 1, expiresAt: 99999, payloadAvailable: true },
			{ id: 'wfa_2', runId: 'wfr_1', nodeId: 'review', attemptId: 1, name: 'log', kind: 'text', contentHash: 'def', size: 10, createdAt: 1, payloadAvailable: false },
		]);
		render(<MemoryRouter><Workflows /></MemoryRouter>);

		await screen.findByRole('region', { name: 'Workflow artifacts' });
		expect(screen.getByText('report')).toBeInTheDocument();
		expect(screen.getByText(/json · 2\.0 KB/)).toBeInTheDocument();
		// Retained payload has a download link.
		expect(screen.getByRole('link', { name: 'Download' })).toHaveAttribute('href', '/api/workflow-runs/wfr_1/artifacts/wfa_1/download');
		// Cleaned-up payload is shown as gone, no download.
		expect(screen.getByText('Payload cleaned up')).toBeInTheDocument();
	});

	it('shows resource pool held and waiting capacity', async () => {
		const poolRun: WorkflowRunDetail = {
			...activeRun,
			resources: [
				{ pool: '', capacity: 2, held: 1 },
				{ pool: 'compiler', capacity: 1, held: 1, waiting: ['ship'] },
			],
		};
		apiMock.runs.mockResolvedValue([poolRun]);
		apiMock.run.mockResolvedValue(poolRun);
		render(<MemoryRouter><Workflows /></MemoryRouter>);

		expect(await screen.findByRole('region', { name: 'Workflow run graph' })).toBeInTheDocument();
		const pools = screen.getByRole('list', { name: 'Resource pools' });
		expect(pools).toHaveTextContent('run concurrency');
		expect(pools).toHaveTextContent('1/2 held');
		expect(pools).toHaveTextContent('compiler');
		expect(pools).toHaveTextContent('waiting: ship');
	});

	it('collapses and expands mapped items and links child and joined results', async () => {
		const user = userEvent.setup();
		const mapVersion: WorkflowVersion = {
			...version,
			definition: {
				...version.definition,
				nodes: [
					{ id: 'fan', name: 'Fan', type: 'map', map: { source: 'items', key: 'id', join: 'join', subworkflow: { workflowId: 'item' } } },
					{ id: 'join', name: 'Join', type: 'join', join: { policy: 'always' } },
				],
				dependencies: [{ from: 'fan', to: 'join' }],
			},
		};
		const mapRun: WorkflowRunDetail = {
			...activeRun,
			version: mapVersion,
			children: [
				{ mapNode: 'fan', key: 'a', index: 0, childRunId: 'wfr_child_a', state: 'successful' },
				{ mapNode: 'fan', key: 'b', index: 1, childRunId: 'wfr_child_b', state: 'failed' },
			],
			nodes: [
				{ nodeId: 'fan', name: 'Fan', type: 'map', state: 'successful', attempts: [{ id: 1, seq: 1, state: 'successful', startedAt: 1 }] },
				{
					nodeId: 'join', name: 'Join', type: 'join', state: 'successful',
					attempts: [{ id: 2, seq: 1, state: 'successful', startedAt: 1, outputs: { result: { policy: 'always', success: 1, failed: 1, total: 2, items: [{ key: 'a', index: 0, state: 'successful' }, { key: 'b', index: 1, state: 'failed' }] } } }],
				},
			],
		};
		apiMock.versions.mockResolvedValue([mapVersion]);
		apiMock.runs.mockResolvedValue([mapRun]);
		apiMock.run.mockResolvedValue(mapRun);
		render(<MemoryRouter><Workflows /></MemoryRouter>);

		// Collapsed by default: item rows hidden, summary shown.
		const toggle = await screen.findByRole('button', { name: /Expand 2 mapped items/ });
		expect(screen.queryByTestId('workflow-map-items')).not.toBeInTheDocument();

		// Expand reveals each item with its stable key, state, and child link.
		await user.click(toggle);
		const items = screen.getByTestId('workflow-map-items');
		expect(items).toHaveTextContent('a');
		expect(items).toHaveTextContent('b');
		expect(screen.getAllByRole('button', { name: 'Open item run' })).toHaveLength(2);

		// Opening an item run selects the child run.
		await user.click(screen.getAllByRole('button', { name: 'Open item run' })[0]);
		await waitFor(() => expect(apiMock.run).toHaveBeenCalledWith('wfr_child_a'));

		// Join renders the input-ordered per-item statuses.
		const join = screen.getByTestId('workflow-join');
		expect(join).toHaveTextContent('always');
		expect(join).toHaveTextContent('1/2 succeeded');
		expect(join).toHaveTextContent('a: successful');
		expect(join).toHaveTextContent('b: failed');

		// Collapse hides the items again.
		await user.click(screen.getByRole('button', { name: /Collapse 2 mapped items/ }));
		expect(screen.queryByTestId('workflow-map-items')).not.toBeInTheDocument();
	});

	it('links a mapped child run back to its parent map node', async () => {
		const user = userEvent.setup();
		const childRun: WorkflowRunDetail = {
			...activeRun,
			id: 'wfr_child_a', parentRunId: 'wfr_1', parentNodeId: 'fan', itemKey: 'a', itemIndex: 0,
			nodes: [{ nodeId: 'work', name: 'Work', type: 'agent', state: 'successful', attempts: [{ id: 3, seq: 1, state: 'successful', startedAt: 1 }] }],
		};
		apiMock.runs.mockResolvedValue([childRun]);
		apiMock.run.mockResolvedValue(childRun);
		render(<MemoryRouter><Workflows /></MemoryRouter>);

		await screen.findByRole('region', { name: 'Workflow run graph' });
		expect(screen.getByText(/Mapped item/)).toHaveTextContent('a');
		await user.click(screen.getByRole('button', { name: /parent run fan/ }));
		await waitFor(() => expect(apiMock.run).toHaveBeenCalledWith('wfr_1'));
	});

	it('links agent attempts and shows live and collected state', async () => {
		const agentVersion: WorkflowVersion = {
			...version,
			definition: { ...version.definition, nodes: [{ id: 'agent', name: 'Implement', type: 'agent', agent: { directory: '/repo', prompt: 'work' } }], dependencies: [] },
		};
		const agentRun: WorkflowRunDetail = {
			...activeRun,
			version: agentVersion,
			nodes: [{ nodeId: 'agent', name: 'Implement', type: 'agent', state: 'running', attempts: [{ id: 2, seq: 1, state: 'running', startedAt: 1, platform: 'any-platform', sessionId: 'session 1', sessionState: 'busy', outputs: { message: 'done' } }] }],
		};
		apiMock.versions.mockResolvedValue([agentVersion]);
		apiMock.runs.mockResolvedValue([agentRun]);
		apiMock.run.mockResolvedValue(agentRun);
		render(<MemoryRouter><Workflows /></MemoryRouter>);

		expect(await screen.findByRole('link', { name: 'Open agent session' })).toHaveAttribute('href', '/session/session%201');
		expect(screen.getByText('Session: busy')).toBeInTheDocument();
		expect(screen.getByText('message: "done"')).toBeInTheDocument();
		connectListeners[0]();
		await waitFor(() => expect(apiMock.versions.mock.calls.length).toBeGreaterThan(1));
	});

	it('shows recovery state and lets users resolve an unknown attempt', async () => {
		const unknownRun: WorkflowRunDetail = {
			...activeRun,
			state: 'paused',
			nodes: [{ nodeId: 'command', name: 'Commit', type: 'command', state: 'unknown', attempts: [{ id: 9, seq: 1, state: 'unknown', startedAt: 1, error: 'command interrupted by server restart' }] }],
		};
		apiMock.runs.mockResolvedValue([unknownRun]);
		apiMock.run.mockResolvedValue(unknownRun);
		render(<MemoryRouter><Workflows /></MemoryRouter>);

		await screen.findByText('unknown');
		expect(screen.getByText(/command interrupted by server restart/)).toBeInTheDocument();
		await userEvent.setup().click(screen.getByRole('button', { name: 'Retry safely' }));
		expect(apiMock.resolveUnknown).toHaveBeenCalledWith('wfr_1', 9, 'retry');
	});
});
