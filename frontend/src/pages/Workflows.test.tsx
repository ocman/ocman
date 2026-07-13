// @vitest-environment jsdom
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkflowRunDetail, WorkflowVersion } from '../lib/api';

const { apiMock, useWorkflowsMock, listeners, connectListeners } = vi.hoisted(() => ({
	apiMock: {
		versions: vi.fn(), publish: vi.fn(), start: vi.fn(), runs: vi.fn(), run: vi.fn(), approve: vi.fn(), pause: vi.fn(), cancel: vi.fn(),
	},
	useWorkflowsMock: vi.fn(() => true),
	listeners: [] as Array<(runId: string) => void>,
	connectListeners: [] as Array<() => void>,
}));

vi.mock('../lib/api', () => ({ api: { workflows: apiMock } }));
vi.mock('../lib/useCapabilities', () => ({ useWorkflows: useWorkflowsMock }));
vi.mock('../lib/useGlobalEvents', () => ({
	onWorkflowRunUpdated: (listener: (runId: string) => void) => {
		listeners.push(listener);
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
		nodes: [{ id: 'review', name: 'Review', type: 'approval' }, { id: 'ship', name: 'Ship', type: 'approval' }],
		dependencies: [{ from: 'review', to: 'ship' }],
	},
};

const activeRun: WorkflowRunDetail = {
	id: 'wfr_1', workflowId: 'release', versionId: version.id, state: 'active', createdAt: 1, updatedAt: 1, version,
	nodes: [
		{ nodeId: 'review', name: 'Review', type: 'approval', state: 'ready', attempts: [{ id: 1, seq: 1, state: 'waiting', startedAt: 1 }] },
		{ nodeId: 'ship', name: 'Ship', type: 'approval', state: 'pending', attempts: [] },
	],
};

describe('Workflows', () => {
	beforeEach(() => {
		vi.clearAllMocks();
		listeners.length = 0;
		connectListeners.length = 0;
		useWorkflowsMock.mockReturnValue(true);
		apiMock.versions.mockResolvedValue([version]);
		apiMock.runs.mockResolvedValue([activeRun]);
		apiMock.run.mockResolvedValue(activeRun);
		apiMock.publish.mockResolvedValue(version);
		apiMock.start.mockResolvedValue(activeRun);
		apiMock.approve.mockResolvedValue({ ...activeRun, nodes: [{ ...activeRun.nodes[0], state: 'successful' }, { ...activeRun.nodes[1], state: 'ready' }] });
	});

	it('is capability gated', () => {
		useWorkflowsMock.mockReturnValue(false);
		render(<Workflows />);
		expect(screen.getByText('Workflows are unavailable on this host.')).toBeInTheDocument();
		expect(apiMock.versions).not.toHaveBeenCalled();
	});

	it('publishes, starts, approves, and refreshes from SSE', async () => {
		const user = userEvent.setup();
		render(<Workflows />);

		await screen.findByRole('region', { name: 'Workflow run graph' });
		expect(screen.getByText('Attempt 1: waiting')).toBeInTheDocument();
		await user.click(screen.getByRole('button', { name: 'Approve Review' }));
		expect(apiMock.approve).toHaveBeenCalledWith('wfr_1', 'review');

		await user.click(screen.getByRole('button', { name: 'Publish and start' }));
		expect(apiMock.publish).toHaveBeenCalledWith(expect.stringContaining('"concurrency": 1'));
		expect(apiMock.start).toHaveBeenCalledWith('wfv_1');

		listeners[0]('another-run');
		await waitFor(() => expect(apiMock.run).toHaveBeenLastCalledWith('wfr_1'));
		connectListeners[0]();
		await waitFor(() => expect(apiMock.versions).toHaveBeenCalledTimes(5));
	});
});
