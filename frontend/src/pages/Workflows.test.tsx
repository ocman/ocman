// @vitest-environment jsdom
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkflowRunDetail, WorkflowVersion } from '../lib/api';

class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

vi.stubGlobal('ResizeObserver', ResizeObserver);

const { apiMock, useWorkflowsMock, listeners, triggerListeners, connectListeners } = vi.hoisted(() => ({
  apiMock: {
    models: vi.fn(),
    versions: vi.fn(),
    validate: vi.fn(),
    publish: vi.fn(),
    activate: vi.fn(),
    deactivate: vi.fn(),
    archive: vi.fn(),
    startActive: vi.fn(),
    start: vi.fn(),
    runs: vi.fn(),
    run: vi.fn(),
    approve: vi.fn(),
    pause: vi.fn(),
    cancel: vi.fn(),
    resolveUnknown: vi.fn(),
    retryFrom: vi.fn(),
    artifacts: vi.fn(),
    artifactDownloadUrl: vi.fn((runId: string, id: string) => `/api/workflow-runs/${runId}/artifacts/${id}/download`),
  },
  useWorkflowsMock: vi.fn(() => true),
  listeners: [] as Array<(runId: string) => void>,
  triggerListeners: [] as Array<() => void>,
  connectListeners: [] as Array<() => void>,
}));

vi.mock('../lib/api', () => ({
  api: {
    models: apiMock.models,
    workflows: apiMock,
    dagu: { status: vi.fn().mockResolvedValue({ status: 'compatible', version: '2.1.0', installCommand: 'brew install dagu' }) },
  },
}));
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
  id: 'wfv_1',
  workflowId: 'release',
  name: 'Release approvals',
  revision: 1,
  createdAt: 1,
  active: true,
  definition: {
    id: 'release',
    name: 'Release approvals',
    version: '1',
    concurrency: 1,
    directory: '/repos/ocman',
    triggers: [
      { id: 'manual', type: 'manual' },
      { id: 'timer', type: 'interval', intervalSeconds: 60, overlap: 'queue' },
    ],
    nodes: [
      { id: 'review', name: 'Review', type: 'approval' },
      { id: 'ship', name: 'Ship', type: 'approval' },
    ],
    dependencies: [{ from: 'review', to: 'ship' }],
  },
  triggerStates: [
    { id: 'manual', type: 'manual', overlap: 'skip', versionId: 'wfv_1', queued: 0 },
    {
      id: 'timer',
      type: 'interval',
      intervalSeconds: 60,
      overlap: 'queue',
      versionId: 'wfv_1',
      nextCheckAt: 120000,
      lastFiredAt: 60000,
      lastDecision: 'queued',
      lastRunId: 'wfr_1',
      queued: 1,
    },
  ],
};

const activeRun: WorkflowRunDetail = {
  id: 'wfr_1',
  workflowId: 'release',
  versionId: version.id,
  state: 'active',
  createdAt: 1,
  updatedAt: 1,
  version,
  trigger: {
    id: 'timer',
    type: 'interval',
    intervalSeconds: 60,
    overlap: 'queue',
    versionId: 'wfv_1',
    firedAt: 60000,
    detail: 'scheduled (every 1m0s)',
  },
  nodes: [
    {
      nodeId: 'review',
      name: 'Review',
      type: 'approval',
      state: 'ready',
      result: { id: 'review', name: 'Review', started: null, ended: null, status: 'ready', output: null },
      attempts: [{ id: 1, seq: 1, state: 'waiting', startedAt: 1 }],
    },
    {
      nodeId: 'ship',
      name: 'Ship',
      type: 'approval',
      state: 'pending',
      result: { id: 'ship', name: 'Ship', started: null, ended: null, status: 'pending', output: null },
      attempts: [],
    },
  ],
};

async function openRunDetails(user: ReturnType<typeof userEvent.setup>, id = 'wfr_1') {
  await user.click(screen.getByRole('tab', { name: 'Run history' }));
  await user.click(screen.getByRole('button', { name: new RegExp(id) }));
  await screen.findByRole('dialog', { name: 'Workflow run details' });
}

describe('Workflows', { timeout: 10_000 }, () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listeners.length = 0;
    triggerListeners.length = 0;
    connectListeners.length = 0;
    useWorkflowsMock.mockReturnValue(true);
    apiMock.models.mockResolvedValue([
      { provider: 'openai', model: 'gpt-5', count: 2, tokensIn: 0, tokensOut: 0, cacheRead: 0, cacheWrite: 0 },
    ]);
    apiMock.versions.mockResolvedValue([version]);
    apiMock.runs.mockResolvedValue([activeRun]);
    apiMock.run.mockResolvedValue(activeRun);
    apiMock.publish.mockResolvedValue(version);
    apiMock.validate.mockResolvedValue({
      definition: version.definition,
      canonicalJson: version.definition,
      yaml: 'id: release\n',
    });
    apiMock.activate.mockResolvedValue(version);
    apiMock.deactivate.mockResolvedValue({ ...version, active: false });
    apiMock.startActive.mockResolvedValue(activeRun);
    apiMock.start.mockResolvedValue(activeRun);
    apiMock.approve.mockResolvedValue({
      ...activeRun,
      nodes: [
        { ...activeRun.nodes[0], state: 'successful' },
        { ...activeRun.nodes[1], state: 'ready' },
      ],
    });
    apiMock.resolveUnknown.mockResolvedValue(activeRun);
    apiMock.artifacts.mockResolvedValue([]);
  });

  it('is capability gated', () => {
    useWorkflowsMock.mockReturnValue(false);
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    expect(screen.getByText('Workflows are unavailable on this host.')).toBeInTheDocument();
    expect(apiMock.versions).not.toHaveBeenCalled();
  });

  it('finds workflows and filters the run queue by state', async () => {
    const user = userEvent.setup();
    const failedRun: WorkflowRunDetail = { ...activeRun, id: 'wfr_2', workflowId: 'nightly', state: 'failed' };
    apiMock.runs.mockResolvedValue([activeRun, failedRun]);
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );

    await user.click(screen.getByRole('tab', { name: 'Run history' }));
    await screen.findByRole('region', { name: 'Workflow runs' });
    await user.type(screen.getByRole('searchbox', { name: 'Find workflows' }), 'nightly');
    expect(screen.getByText('nightly')).toBeInTheDocument();
    expect(screen.queryByText('release', { selector: 'td' })).not.toBeInTheDocument();

    await user.clear(screen.getByRole('searchbox', { name: 'Find workflows' }));
    await user.selectOptions(screen.getByRole('combobox', { name: 'Run state' }), 'failed');
    expect(screen.getByText('nightly')).toBeInTheDocument();
    expect(screen.queryByText('release', { selector: 'td' })).not.toBeInTheDocument();
  });

  it('shows and filters workflows by project', async () => {
    const other = {
      ...version,
      id: 'wfv_2',
      workflowId: 'other',
      name: 'Other workflow',
      definition: { ...version.definition, id: 'other', directory: '/repos/other' },
    };
    apiMock.versions.mockResolvedValue([version, other]);
    render(
      <MemoryRouter initialEntries={['/workflows?project=%2Frepos%2Fother']}>
        <Workflows />
      </MemoryRouter>,
    );

    expect(await screen.findByRole('button', { name: 'Other workflow' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Release approvals' })).not.toBeInTheDocument();
  });

  it('shows workflows and run history as separate tables', async () => {
    const user = userEvent.setup();
    const failedRun: WorkflowRunDetail = { ...activeRun, id: 'wfr_2', workflowId: 'nightly', state: 'failed' };
    apiMock.runs.mockResolvedValue([activeRun, failedRun]);
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );

    expect(await screen.findByRole('columnheader', { name: 'Workflow' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Revision' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Release approvals' }));
    expect(screen.getByRole('columnheader', { name: 'Started' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /Run history.*release/ })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Clear filters' }));
    await user.click(screen.getByRole('button', { name: /nightly.*wfr_2/ }));
    expect(apiMock.run).toHaveBeenCalledWith('wfr_2');
    expect(await screen.findByRole('dialog', { name: 'Workflow run details' })).toBeInTheDocument();
  });

  it('restores the selected workflow run from the URL', async () => {
    render(
      <MemoryRouter initialEntries={['/workflows?tab=runs&workflow=release&run=wfr_1']}>
        <Workflows />
      </MemoryRouter>,
    );

    expect(await screen.findByRole('heading', { name: /Run history.*release/ })).toBeInTheDocument();
    expect(await screen.findByRole('dialog', { name: 'Workflow run details' })).toBeInTheDocument();
  });

  it('surfaces approvals and running nodes as shortcuts to their details', async () => {
    const user = userEvent.setup();
    const run = {
      ...activeRun,
      version: {
        ...version,
        definition: {
          ...version.definition,
          nodes: [...version.definition.nodes, { id: 'build', name: 'Build release', type: 'command' as const }],
        },
      },
      nodes: [
        ...activeRun.nodes,
        {
          nodeId: 'build',
          name: 'Build release',
          type: 'command' as const,
          state: 'running' as const,
          result: { id: 'build', name: 'Build release', started: 'now', ended: null, status: 'running' as const, output: null },
          attempts: [{ id: 2, seq: 1, state: 'running' as const, startedAt: 2 }],
        },
      ],
    };
    apiMock.run.mockResolvedValue(run);
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );

    await openRunDetails(user);
    const activity = screen.getByRole('region', { name: 'Run activity' });
    expect(activity).toHaveTextContent('Needs approval 1');
    expect(activity).toHaveTextContent('Running now 1');
    await user.click(screen.getByRole('button', { name: 'View running node Build release' }));
    expect(screen.getByRole('complementary', { name: 'Selected node details' })).toHaveTextContent('Build release');
    await user.click(screen.getByRole('button', { name: 'View approval Review' }));
    expect(screen.getByRole('button', { name: 'Approve Review' })).toBeInTheDocument();
  });

  it('restores an editor version from the URL', async () => {
    render(
      <MemoryRouter initialEntries={['/workflows?view=author&version=wfv_1']}>
        <Workflows />
      </MemoryRouter>,
    );

    expect(await screen.findByRole('region', { name: 'Workflow authoring' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete workflow' })).toBeInTheDocument();
  });

  it('shows only the latest revision of each workflow', async () => {
    apiMock.versions.mockResolvedValue([
      { ...version, active: false },
      { ...version, id: 'wfv_2', name: 'Release approvals v2', revision: 2 },
    ]);
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );

    expect(await screen.findByRole('button', { name: 'Release approvals v2' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Release approvals' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('checkbox', { name: 'Show revisions' }));
    expect(screen.getByRole('button', { name: 'Release approvals' })).toBeInTheDocument();
  });

  it('edits a workflow by preloading its existing definition', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );

    await user.click(await screen.findByRole('button', { name: 'Edit workflow' }));
    expect(screen.getByRole('heading', { name: 'Author workflow' })).toBeInTheDocument();
    await user.click(screen.getByRole('tab', { name: 'YAML' }));
    expect(screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Workflow YAML or JSON' }).value).toContain(
      '"id": "release"',
    );
  });

  it('archives a workflow from the editor', async () => {
    const user = userEvent.setup();
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    apiMock.archive.mockResolvedValue(undefined);
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );

    await user.click(await screen.findByRole('button', { name: 'Edit workflow' }));
    await user.click(screen.getByRole('button', { name: 'Delete workflow' }));
    expect(apiMock.archive).toHaveBeenCalledWith(version.id);
    expect(await screen.findByRole('button', { name: 'New workflow' })).toBeInTheDocument();
  });

  it('opens a definition with null dependencies', async () => {
    const user = userEvent.setup();
    apiMock.versions.mockResolvedValue([{ ...version, definition: { ...version.definition, dependencies: null } }]);
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await user.click(await screen.findByRole('button', { name: 'Edit workflow' }));
    expect(await screen.findByRole('region', { name: 'Workflow builder' })).toBeInTheDocument();
  });

  it('closes run details with Escape', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );

    await openRunDetails(user);
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog', { name: 'Workflow run details' })).not.toBeInTheDocument();
  });

  it('deactivates a workflow from its detail view', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );

    await user.click(await screen.findByRole('button', { name: 'Edit workflow' }));
    await user.click(screen.getByRole('button', { name: 'Deactivate' }));
    expect(apiMock.deactivate).toHaveBeenCalledWith('wfv_1');
  });

  it('validates source into the visual builder and reports source errors', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );

    await user.click(screen.getByRole('tab', { name: 'Workflows' }));
    await user.click(screen.getByRole('button', { name: 'New workflow' }));
    expect(await screen.findByRole('region', { name: 'Workflow builder' })).toHaveTextContent('Workflow editor');
    await user.click(screen.getByRole('tab', { name: 'YAML' }));
    expect(screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Workflow YAML or JSON' }).value).toContain(
      'id: release',
    );
    apiMock.validate.mockRejectedValue(new Error('line 2, column 1: duplicate key "name"'));
    await user.clear(screen.getByRole('textbox', { name: 'Workflow YAML or JSON' }));
    await user.type(screen.getByRole('textbox', { name: 'Workflow YAML or JSON' }), 'name: one\nname: two');
    await user.click(screen.getByRole('button', { name: 'Validate' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Validation failed: line 2, column 1: duplicate key "name" Hint: Remove or rename the duplicate key.',
    );
  });

  it('builds agent nodes into the canonical workflow source', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await user.click(screen.getByRole('button', { name: 'New workflow' }));
    await screen.findByRole('region', { name: 'Workflow builder' });
    await user.click(screen.getByRole('button', { name: '+ Add' }));
    await user.click(screen.getByRole('menuitem', { name: 'agent' }));
    expect(screen.getByRole('complementary', { name: 'Node properties' })).toHaveTextContent('Agent');
    await user.type(screen.getByLabelText('Prompt'), 'Review the change');
    await user.click(screen.getByRole('combobox', { name: 'Model' }));
    await user.type(screen.getByRole('textbox', { name: 'Search models' }), 'opnai');
    await user.click(screen.getByRole('option', { name: 'openai/gpt-5' }));
    await user.type(screen.getByLabelText('Variant'), 'high');
    await user.type(screen.getByLabelText('Agent'), 'build');
    await user.click(screen.getByRole('tab', { name: 'YAML' }));
    const source = screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Workflow YAML or JSON' }).value;
    expect(source).toContain('Review the change');
    expect(source).toContain('"model": "openai/gpt-5"');
    expect(source).toContain('"reasoning": "high"');
    expect(source).toContain('"agent": "build"');
    await user.click(screen.getByRole('tab', { name: 'Editor' }));
    await user.click(screen.getByRole('button', { name: 'Delete node' }));
    expect(screen.getByRole('complementary', { name: 'Workflow properties' })).toHaveTextContent(
      'Triggers and schedule',
    );
  });

  it('builds command nodes into the canonical workflow source', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await user.click(screen.getByRole('button', { name: 'New workflow' }));
    await user.click(screen.getByRole('button', { name: '+ Add' }));
    await user.click(screen.getByRole('menuitem', { name: 'command' }));
    await user.type(screen.getByLabelText('Command'), 'pnpm test');
    await user.click(screen.getByRole('tab', { name: 'YAML' }));
    expect(screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Workflow YAML or JSON' }).value).toContain(
      'pnpm test',
    );
  });

  it('builds subworkflow nodes into the canonical workflow source', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await user.click(screen.getByRole('button', { name: 'New workflow' }));
    await user.click(screen.getByRole('button', { name: '+ Add' }));
    await user.click(screen.getByRole('menuitem', { name: 'subworkflow' }));
    await user.type(screen.getByLabelText('Workflow ID'), 'release-child');
    await user.click(screen.getByRole('tab', { name: 'YAML' }));
    expect(screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Workflow YAML or JSON' }).value).toContain(
      'release-child',
    );
  });

  it('builds map nodes into the canonical workflow source', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await user.click(screen.getByRole('button', { name: 'New workflow' }));
    await user.click(screen.getByRole('button', { name: '+ Add' }));
    await user.click(screen.getByRole('menuitem', { name: 'map' }));
    await user.type(screen.getByLabelText('Items source'), 'nodes.discover.output');
    await user.type(screen.getByLabelText('Item key'), 'id');
    await user.type(screen.getByLabelText('Subworkflow ID'), 'release-item');
    await user.type(screen.getByLabelText('Join node'), 'join_1');
    await user.click(screen.getByRole('tab', { name: 'YAML' }));
    expect(screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Workflow YAML or JSON' }).value).toContain(
      'release-item',
    );
  });

  it('builds join nodes into the canonical workflow source', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await user.click(screen.getByRole('button', { name: 'New workflow' }));
    await user.click(screen.getByRole('button', { name: '+ Add' }));
    await user.click(screen.getByRole('menuitem', { name: 'join' }));
    await user.selectOptions(screen.getByLabelText('Policy'), 'always');
    await user.click(screen.getByRole('tab', { name: 'YAML' }));
    expect(screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Workflow YAML or JSON' }).value).toContain(
      '"policy": "always"',
    );
  });

  it('does not show a stale graph when source changes during validation', async () => {
    const user = userEvent.setup();
    let resolve!: (value: unknown) => void;
    apiMock.validate.mockReturnValueOnce(
      new Promise((done) => {
        resolve = done;
      }),
    );
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await user.click(screen.getByRole('button', { name: 'New workflow' }));
    await user.click(screen.getByRole('tab', { name: 'YAML' }));
    await user.clear(screen.getByRole('textbox', { name: 'Workflow YAML or JSON' }));
    await act(async () =>
      resolve({ definition: version.definition, canonicalJson: version.definition, yaml: 'id: release\n' }),
    );
    expect(screen.queryByRole('region', { name: 'Workflow definition graph' })).not.toBeInTheDocument();
  });

  it('publishes, activates, compares, starts active, approves, and refreshes from SSE', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );

    await openRunDetails(user);
    await screen.findByRole('region', { name: 'Workflow run graph' });
    const status = screen.getByRole('dialog', { name: 'Workflow run details' }).querySelector('.workflow-run-status');
    expect(status).toHaveTextContent('active');
    expect(status?.querySelector('.workflow-run-spinner')).toBeInTheDocument();
    expect(screen.getByText('Attempt 1: waiting')).toBeInTheDocument();
    expect(screen.getAllByText(/timer · interval · queue/)).toHaveLength(1);
    expect(screen.getByText(/scheduled \(every 1m0s\)/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Approve Review' }));
    expect(apiMock.approve).toHaveBeenCalledWith('wfr_1', 'review');

    await user.click(screen.getByRole('tab', { name: 'Workflows' }));
    await user.click(screen.getByRole('button', { name: 'New workflow' }));
    await user.click(screen.getByRole('button', { name: 'Save new version' }));
    expect(apiMock.publish).toHaveBeenCalledWith(expect.stringContaining('concurrency: 1'));
    expect(apiMock.start).not.toHaveBeenCalled();
    await user.click(await screen.findByRole('button', { name: 'Start run' }));
    expect(apiMock.startActive).toHaveBeenCalledWith('release');
    await user.click(await screen.findByRole('button', { name: 'Close' }));
    await user.click(screen.getByRole('tab', { name: 'Workflows' }));
    await user.click(screen.getByRole('button', { name: 'New workflow' }));
    expect(screen.getByRole('group', { name: 'Version comparison' })).toHaveTextContent('Publish another revision');

    listeners[0]('another-run');
    await waitFor(() => expect(apiMock.run).toHaveBeenLastCalledWith('wfr_1'));
    triggerListeners[0]();
    await waitFor(() => expect(apiMock.versions.mock.calls.length).toBeGreaterThan(3));
    connectListeners[0]();
    await waitFor(() => expect(apiMock.versions.mock.calls.length).toBeGreaterThanOrEqual(6));
  });

  it('renders run progress on the dependency graph', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );

    await openRunDetails(user);
    screen.getByRole('region', { name: 'Workflow run graph' });
    const review = screen.getByTestId('rf__node-review');
    const ship = screen.getByTestId('rf__node-ship');
    const y = (node: HTMLElement) => Number(/translate\([^,]+,\s*([\d.-]+)px\)/.exec(node.style.transform)?.[1]);
    expect(review.querySelector('.workflow-canvas-node')).toHaveAttribute('data-state', 'ready');
    expect(ship.querySelector('.workflow-canvas-node')).toHaveAttribute('data-state', 'pending');
    expect(y(review)).toBeLessThan(y(ship));
    expect(y(ship) - y(review)).toBeGreaterThan(250);
  });

  it('publishes automated-only versions without trying a manual start', async () => {
    const user = userEvent.setup();
    apiMock.publish.mockResolvedValue({
      ...version,
      definition: { ...version.definition, triggers: [{ id: 'timer', type: 'interval', intervalSeconds: 60 }] },
    });
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await user.click(screen.getByRole('button', { name: 'New workflow' }));
    await user.click(screen.getByRole('button', { name: 'Save new version' }));
    expect(apiMock.publish).toHaveBeenCalled();
    expect(apiMock.start).not.toHaveBeenCalled();
  });

  it('shows a start-run error in Operations', async () => {
    const user = userEvent.setup();
    apiMock.startActive.mockRejectedValueOnce(new Error('active workflow is unavailable'));
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await user.click(await screen.findByRole('button', { name: 'Start run' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('active workflow is unavailable');
  });
  it('shows command outcomes, logs, and node output', async () => {
    const user = userEvent.setup();
    const commandRun: WorkflowRunDetail = {
      ...activeRun,
      state: 'failed',
      nodes: [
        {
          nodeId: 'test',
          name: 'Run tests',
          type: 'command',
          state: 'failed',
          result: {
            id: 'test',
            name: 'Run tests',
            started: '1970-01-01T00:00:00.001Z',
            ended: '1970-01-01T00:00:00.002Z',
            status: 'failed',
            output: { error: 'exit status 7' },
          },
          attempts: [
            {
              id: 2,
              seq: 1,
              state: 'failed',
              startedAt: 1,
              completedAt: 2,
              exitCode: 7,
              stdout: 'test output',
              stderr: 'test failure',
              error: 'exit status 7',
            },
          ],
        },
      ],
    };
    apiMock.runs.mockResolvedValue([commandRun]);
    apiMock.run.mockResolvedValue(commandRun);
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await openRunDetails(user);

    expect(await screen.findByText(/failed \(exit 7\)/)).toBeInTheDocument();
    expect(screen.getByText('test output')).toBeInTheDocument();
    expect(screen.getByText('test failure')).toBeInTheDocument();
    expect(screen.getByLabelText('node output')).toHaveTextContent('"error": "exit status 7"');
  });

  it('shows workspace shard leases and ownership', async () => {
    const user = userEvent.setup();
    const leasedRun: WorkflowRunDetail = {
      ...activeRun,
      workspace: [
        {
          nodeId: 'edit-a',
          attemptId: 1,
          shard: 0,
          mode: 'path',
          paths: ['src/a'],
          host: 'local-host',
          shardPath: '/worktrees/a',
          acquiredAt: 1,
        },
        { nodeId: 'commit', attemptId: 2, shard: 0, mode: 'exclusive', commit: true, acquiredAt: 2 },
      ],
    };
    apiMock.runs.mockResolvedValue([leasedRun]);
    apiMock.run.mockResolvedValue(leasedRun);
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await openRunDetails(user);

    await screen.findByRole('list', { name: 'Workspace leases' });
    expect(screen.getByText(/shard 0 · path/)).toBeInTheDocument();
    expect(screen.getByText(/src\/a/)).toBeInTheDocument();
    expect(screen.getByText(/host local-host/)).toBeInTheDocument();
    expect(screen.getByText(/\/worktrees\/a/)).toBeInTheDocument();
    expect(screen.getByText(/shard 0 · exclusive \(commit\)/)).toBeInTheDocument();
  });

  it('inspects artifact metadata and offers retained-payload download', async () => {
    const user = userEvent.setup();
    apiMock.artifacts.mockResolvedValue([
      {
        id: 'wfa_1',
        runId: 'wfr_1',
        nodeId: 'review',
        attemptId: 1,
        name: 'report',
        kind: 'json',
        contentHash: 'abc',
        size: 2048,
        createdAt: 1,
        expiresAt: 99999,
        payloadAvailable: true,
      },
      {
        id: 'wfa_2',
        runId: 'wfr_1',
        nodeId: 'review',
        attemptId: 1,
        name: 'log',
        kind: 'text',
        contentHash: 'def',
        size: 10,
        createdAt: 1,
        payloadAvailable: false,
      },
    ]);
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await openRunDetails(user);

    await screen.findByRole('region', { name: 'Historical workflow artifacts' });
    expect(screen.getByText('report')).toBeInTheDocument();
    expect(screen.getByText(/json · 2\.0 KB/)).toBeInTheDocument();
    expect(screen.getAllByText(/review attempt 1/)).toHaveLength(2);
    // Retained payload has a download link.
    expect(screen.getByRole('link', { name: 'Download' })).toHaveAttribute(
      'href',
      '/api/workflow-runs/wfr_1/artifacts/wfa_1/download',
    );
    // Cleaned-up payload is shown as gone, no download.
    expect(screen.getByText('Payload cleaned up')).toBeInTheDocument();
  });

  it('shows resource pool held and waiting capacity', async () => {
    const user = userEvent.setup();
    const poolRun: WorkflowRunDetail = {
      ...activeRun,
      resources: [
        { pool: '', capacity: 2, held: 1 },
        { pool: 'compiler', capacity: 1, held: 1, waiting: ['ship'] },
      ],
    };
    apiMock.runs.mockResolvedValue([poolRun]);
    apiMock.run.mockResolvedValue(poolRun);
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await openRunDetails(user);

    expect(await screen.findByRole('region', { name: 'Workflow run graph' })).toBeInTheDocument();
    const pools = screen.getByRole('region', { name: 'Resource pools' });
    expect(pools).toHaveTextContent('Run capacity');
    expect(pools).toHaveTextContent('1 of 2 in use');
    expect(pools).toHaveTextContent('compiler');
    expect(pools).toHaveTextContent('Waiting: ship');
  });

  it('collapses and expands mapped items and links child and joined results', async () => {
    const user = userEvent.setup();
    const mapVersion: WorkflowVersion = {
      ...version,
      definition: {
        ...version.definition,
        nodes: [
          {
            id: 'fan',
            name: 'Fan',
            type: 'map',
            map: { source: '${nodes.seed.output}', key: 'id', join: 'join', subworkflow: { workflowId: 'item' } },
          },
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
        {
          nodeId: 'fan',
          name: 'Fan',
          type: 'map',
          state: 'successful',
          result: {
            id: 'fan',
            name: 'Fan',
            started: null,
            ended: null,
            status: 'successful',
            output: {
              items: [
                { key: 'a', index: 0, status: 'successful', output: { ok: true } },
                { key: 'b', index: 1, status: 'failed', output: { error: 'failed' } },
              ],
            },
          },
          attempts: [{ id: 1, seq: 1, state: 'successful', startedAt: 1 }],
        },
        {
          nodeId: 'join',
          name: 'Join',
          type: 'join',
          state: 'successful',
          result: {
            id: 'join',
            name: 'Join',
            started: null,
            ended: null,
            status: 'successful',
            output: {
              policy: 'always',
              success: 1,
              failed: 1,
              total: 2,
              items: [
                { key: 'a', index: 0, status: 'successful', output: { ok: true } },
                { key: 'b', index: 1, status: 'failed', output: { error: 'failed' } },
              ],
              error: 'join policy failed',
            },
          },
          attempts: [{ id: 2, seq: 1, state: 'successful', startedAt: 1 }],
        },
      ],
    };
    apiMock.versions.mockResolvedValue([mapVersion]);
    apiMock.runs.mockResolvedValue([mapRun]);
    apiMock.run.mockResolvedValue(mapRun);
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await openRunDetails(user);

    // Collapsed by default: item rows hidden, summary shown.
    const toggle = await screen.findByRole('button', { name: /Expand 2 mapped items/ });
    expect(screen.getByTestId('rf__node-fan').querySelector('.workflow-canvas-node')).toHaveAttribute(
      'data-state',
      'successful',
    );
    expect(screen.getByText('Phase 1 · map · fan')).toBeInTheDocument();
    expect(screen.queryByTestId('workflow-map-items')).not.toBeInTheDocument();

    // Expand reveals each item with its stable key, state, and child link.
    await user.click(toggle);
    const items = screen.getByTestId('workflow-map-items');
    expect(items).toHaveTextContent('a');
    expect(items).toHaveTextContent('b');
    expect(screen.getByLabelText('a output')).toHaveTextContent('"ok": true');
    expect(screen.getAllByRole('button', { name: 'Open item run' })).toHaveLength(2);

    // Opening an item run selects the child run.
    await user.click(screen.getAllByRole('button', { name: 'Open item run' })[0]);
    await waitFor(() => expect(apiMock.run).toHaveBeenCalledWith('wfr_child_a'));

    // Join renders the input-ordered per-item statuses.
    await user.click(screen.getByTestId('rf__node-join').querySelector('.workflow-canvas-node')!);
    const join = screen.getByTestId('workflow-join');
    expect(join).toHaveTextContent('always');
    expect(join).toHaveTextContent('1/2 succeeded');
    expect(join).toHaveTextContent('a: successful');
    expect(join).toHaveTextContent('b: failed');
    expect(join).toHaveTextContent('join policy failed');
    expect(screen.getByLabelText('b joined output')).toHaveTextContent('"error": "failed"');

    // Switching inspector nodes resets the map list to its collapsed state.
    await user.click(screen.getByTestId('rf__node-fan').querySelector('.workflow-canvas-node')!);
    expect(screen.queryByTestId('workflow-map-items')).not.toBeInTheDocument();
  });

  it('links a mapped child run back to its parent map node', async () => {
    const user = userEvent.setup();
    const childRun: WorkflowRunDetail = {
      ...activeRun,
      id: 'wfr_child_a',
      parentRunId: 'wfr_1',
      parentNodeId: 'fan',
      itemKey: 'a',
      itemIndex: 0,
      nodes: [
        {
          nodeId: 'work',
          name: 'Work',
          type: 'agent',
          state: 'successful',
          result: { id: 'work', name: 'Work', started: null, ended: null, status: 'successful', output: null },
          attempts: [{ id: 3, seq: 1, state: 'successful', startedAt: 1 }],
        },
      ],
    };
    apiMock.runs.mockResolvedValue([childRun]);
    apiMock.run.mockResolvedValue(childRun);
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await openRunDetails(user, 'wfr_child_a');

    await screen.findByRole('region', { name: 'Workflow run graph' });
    expect(screen.getByText(/Mapped item/)).toHaveTextContent('a');
    await user.click(screen.getByRole('button', { name: /parent run fan/ }));
    await waitFor(() => expect(apiMock.run).toHaveBeenCalledWith('wfr_1'));
  });

  it('links agent attempts and shows live state', async () => {
    const user = userEvent.setup();
    const agentVersion: WorkflowVersion = {
      ...version,
      definition: {
        ...version.definition,
        nodes: [{ id: 'agent', name: 'Implement', type: 'agent', agent: { directory: '/repo', prompt: 'work' } }],
        dependencies: [],
      },
    };
    const agentRun: WorkflowRunDetail = {
      ...activeRun,
      version: agentVersion,
      nodes: [
        {
          nodeId: 'agent',
          name: 'Implement',
          type: 'agent',
          state: 'running',
          result: {
            id: 'agent',
            name: 'Implement',
            started: '1970-01-01T00:00:00.001Z',
            ended: null,
            status: 'running',
            output: null,
          },
          attempts: [
            {
              id: 2,
              seq: 1,
              state: 'running',
              startedAt: 1,
              platform: 'any-platform',
              sessionId: 'session 1',
              sessionState: 'busy',
            },
          ],
        },
      ],
    };
    apiMock.versions.mockResolvedValue([agentVersion]);
    apiMock.runs.mockResolvedValue([agentRun]);
    apiMock.run.mockResolvedValue(agentRun);
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await openRunDetails(user);

    expect(await screen.findByRole('link', { name: 'Open agent session' })).toHaveAttribute(
      'href',
      '/session/session%201',
    );
    expect(screen.getByText('Session: busy')).toBeInTheDocument();
    connectListeners[0]();
    await waitFor(() => expect(apiMock.versions.mock.calls.length).toBeGreaterThan(1));
  });

  it('shows recovery state and lets users resolve an unknown attempt', async () => {
    const user = userEvent.setup();
    const unknownRun: WorkflowRunDetail = {
      ...activeRun,
      state: 'paused',
      nodes: [
        {
          nodeId: 'command',
          name: 'Commit',
          type: 'command',
          state: 'unknown',
          result: {
            id: 'command',
            name: 'Commit',
            started: '1970-01-01T00:00:00.001Z',
            ended: null,
            status: 'unknown',
            output: { error: 'command interrupted by server restart' },
          },
          attempts: [{ id: 9, seq: 1, state: 'unknown', startedAt: 1, error: 'command interrupted by server restart' }],
        },
      ],
    };
    apiMock.runs.mockResolvedValue([unknownRun]);
    apiMock.run.mockResolvedValue(unknownRun);
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await openRunDetails(user);

    expect(screen.getAllByText('unknown')).not.toHaveLength(0);
    expect(screen.getAllByText(/command interrupted by server restart/)).toHaveLength(2);
    await user.click(screen.getByRole('button', { name: 'Retry safely' }));
    expect(apiMock.resolveUnknown).toHaveBeenCalledWith('wfr_1', 9, 'retry');
  });

  it('retries a settled workflow from the selected node', async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const failedRun: WorkflowRunDetail = {
      ...activeRun,
      state: 'failed',
      nodes: [{ ...activeRun.nodes[0], state: 'failed' }],
    };
    apiMock.runs.mockResolvedValue([failedRun]);
    apiMock.run.mockResolvedValue(failedRun);
    apiMock.retryFrom.mockResolvedValue({ ...failedRun, id: 'wfr_retry', state: 'active' });
    render(
      <MemoryRouter>
        <Workflows />
      </MemoryRouter>,
    );
    await openRunDetails(user);

    await user.click(screen.getByRole('button', { name: `Retry from ${failedRun.nodes[0].name} on revision 1` }));
    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('may repeat external side effects'));
    expect(apiMock.retryFrom).toHaveBeenCalledWith('wfr_1', failedRun.nodes[0].nodeId, 'wfv_1');
    confirm.mockRestore();
  });
});
