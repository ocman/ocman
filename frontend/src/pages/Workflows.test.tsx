// @vitest-environment jsdom
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkflowRunDetail, WorkflowVersion } from '../lib/api';

const { apiMock, useWorkflowsMock, listeners, triggerListeners, connectListeners } = vi.hoisted(() => ({
	apiMock: {
		versions: vi.fn(), publish: vi.fn(), start: vi.fn(), runs: vi.fn(), run: vi.fn(), approve: vi.fn(), pause: vi.fn(), cancel: vi.fn(),
		artifacts: vi.fn(), artifactDownloadUrl: vi.fn((runId: string, id: string) => `/api/workflow-runs/${runId}/artifacts/${id}/download`),
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
	id: 'wfv_1', workflowId: 'release', name: 'Release approvals', revision: 1, createdAt: 1,
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
		apiMock.start.mockResolvedValue(activeRun);
		apiMock.approve.mockResolvedValue({ ...activeRun, nodes: [{ ...activeRun.nodes[0], state: 'successful' }, { ...activeRun.nodes[1], state: 'ready' }] });
		apiMock.artifacts.mockResolvedValue([]);
	});

	it('is capability gated', () => {
		useWorkflowsMock.mockReturnValue(false);
		render(<MemoryRouter><Workflows /></MemoryRouter>);
		expect(screen.getByText('Workflows are unavailable on this host.')).toBeInTheDocument();
		expect(apiMock.versions).not.toHaveBeenCalled();
	});

	it('publishes, starts, approves, and refreshes from SSE', async () => {
		const user = userEvent.setup();
		render(<MemoryRouter><Workflows /></MemoryRouter>);

		await screen.findByRole('region', { name: 'Workflow run graph' });
		expect(screen.getByText('Attempt 1: waiting')).toBeInTheDocument();
		expect(screen.getAllByText(/timer · interval · queue/)).toHaveLength(2);
		expect(screen.getByText(/Last .* \(queued\) · 1 queued/)).toBeInTheDocument();
		expect(screen.getByText(/scheduled \(every 1m0s\)/)).toBeInTheDocument();
		await user.click(screen.getByRole('button', { name: 'Approve Review' }));
		expect(apiMock.approve).toHaveBeenCalledWith('wfr_1', 'review');

		await user.click(screen.getByRole('button', { name: 'Publish workflow' }));
		expect(apiMock.publish).toHaveBeenCalledWith(expect.stringContaining('"concurrency": 1'));
		expect(apiMock.start).toHaveBeenCalledWith('wfv_1');

		listeners[0]('another-run');
		await waitFor(() => expect(apiMock.run).toHaveBeenLastCalledWith('wfr_1'));
		triggerListeners[0]();
		await waitFor(() => expect(apiMock.versions).toHaveBeenCalledTimes(5));
		connectListeners[0]();
		await waitFor(() => expect(apiMock.versions).toHaveBeenCalledTimes(6));
	});

	it('publishes automated-only versions without trying a manual start', async () => {
		const user = userEvent.setup();
		apiMock.publish.mockResolvedValue({ ...version, definition: { ...version.definition, triggers: [{ id: 'timer', type: 'interval', intervalSeconds: 60 }] } });
		render(<Workflows />);
		await screen.findByRole('region', { name: 'Workflow run graph' });
		await user.click(screen.getByRole('button', { name: 'Publish workflow' }));
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
	});
});
