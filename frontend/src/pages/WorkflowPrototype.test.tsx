// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { pipelineState, WorkflowPrototype } from './WorkflowPrototype';

const originalScrollIntoView = Element.prototype.scrollIntoView;

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  if (originalScrollIntoView) Element.prototype.scrollIntoView = originalScrollIntoView;
  else delete (Element.prototype as Partial<Element>).scrollIntoView;
});

describe('WorkflowPrototype', () => {
  it('keeps independent YAML and JSON drafts with equivalent fixture semantics', async () => {
    const user = userEvent.setup();
    render(<WorkflowPrototype />);

    expect(screen.getByRole('tablist', { name: 'Workflow prototype view' })).toBeInTheDocument();
    expect(screen.getByRole('tablist', { name: 'Source format' })).toBeInTheDocument();
    const source = screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Workflow source' });
    expect(source.value).toContain('version: 7');
    expect(source.value).toContain('run: scripts/discover-migration-units');
    expect(source.value).toContain('key: item.path');
    await user.type(source, '\n# local edit');
    expect(screen.getByText('YAML draft edited · validation not wired')).toBeInTheDocument();

    await user.click(screen.getByRole('tab', { name: 'JSON' }));
    const jsonSource = screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Workflow source' });
    expect(jsonSource.value).toContain('"version": 7');
    expect(jsonSource.value).toContain('"run": "scripts/discover-migration-units"');
    expect(jsonSource.value).toContain('"key": "item.path"');
    expect(screen.getByText('JSON fixture source · not persisted')).toBeInTheDocument();
    await user.type(jsonSource, '\n ');
    expect(screen.getByText('JSON draft edited · validation not wired')).toBeInTheDocument();

    await user.click(screen.getByRole('tab', { name: 'YAML' }));
    expect(screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Workflow source' }).value).toContain('# local edit');
    expect(screen.getByText('YAML draft edited · validation not wired')).toBeInTheDocument();

    await user.click(screen.getByRole('tab', { name: 'Run graph' }));
    expect(screen.queryByRole('textbox', { name: 'Workflow source' })).not.toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Workflow run graph' })).toBeInTheDocument();
  });

  it.each([
    ['successful', ['successful', 'successful', 'successful', 'successful', 'successful', 'successful']],
    ['pending', ['pending', 'pending', 'pending', 'pending', 'pending', 'pending']],
    ['skipped', ['skipped', 'skipped', 'skipped', 'skipped', 'skipped', 'skipped']],
    ['failed', ['successful', 'successful', 'failed', 'blocked', 'blocked', 'blocked']],
    ['running', ['successful', 'successful', 'running', 'pending', 'pending', 'pending']],
    ['waiting', ['successful', 'waiting', 'pending', 'pending', 'pending', 'pending']],
    ['ready', ['ready', 'pending', 'pending', 'pending', 'pending', 'pending']],
    ['blocked', ['blocked', 'pending', 'pending', 'pending', 'pending', 'pending']],
    ['unknown', ['unknown', 'blocked', 'blocked', 'blocked', 'blocked', 'blocked']],
    ['paused', ['paused', 'pending', 'pending', 'pending', 'pending', 'pending']],
    ['canceled', ['canceled', 'skipped', 'skipped', 'skipped', 'skipped', 'skipped']],
  ] as const)('derives every %s pipeline branch', (state, expected) => {
    expect(expected.map((_, step) => pipelineState(state, step))).toEqual(expected);
  });

  it('distinguishes every workflow state with text and a symbol', async () => {
    const user = userEvent.setup();
    render(<WorkflowPrototype />);
    await user.click(screen.getByRole('tab', { name: 'Run graph' }));

    const legend = screen.getByRole('list', { name: 'Node state legend' });
    const states = {
      Pending: '[ ]', Ready: '>>', Running: 'RUN', Waiting: 'WAIT', Successful: 'OK', Failed: '!!',
      Skipped: '--', Blocked: 'X', Unknown: '?', Paused: '||', Canceled: 'STOP',
    };
    for (const [state, symbol] of Object.entries(states)) {
      expect(within(legend).getByText(state)).toBeInTheDocument();
      expect(within(legend).getByText(symbol)).toBeInTheDocument();
      expect(within(legend).getByLabelText(`State: ${state}`)).toBeInTheDocument();
    }
    expect(screen.getByText('166 node runs')).toBeInTheDocument();
  });

  it('expands mapped branches and keeps selected-node context when collapsed', async () => {
    const user = userEvent.setup();
    render(<WorkflowPrototype />);
    await user.click(screen.getByRole('tab', { name: 'Run graph' }));

    await user.click(screen.getByRole('button', { name: 'Expand 18 migration items' }));
    await user.click(screen.getByRole('button', { name: /^Expand parser.ts branch/ }));
    await user.click(screen.getByRole('button', { name: /^Inspect unit test shard parser.ts/ }));
    expect(screen.getByRole('heading', { name: 'unit test shard parser.ts' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /^Inspect Implement parser.ts/ }));

    const inspector = screen.getByRole('complementary', { name: 'Node details' });
    expect(within(inspector).getByRole('heading', { name: 'Implement parser.ts' })).toBeInTheDocument();
    for (const section of ['Attempts', 'Logs', 'Artifacts', 'Resources', 'Workspace ownership']) {
      expect(within(inspector).getByRole('heading', { name: section })).toBeInTheDocument();
    }
    expect(within(inspector).getByText('parser.ts')).toBeInTheDocument();
    expect(within(inspector).getByText('migrate-unit@4')).toBeInTheDocument();
    expect(within(inspector).getByText(/agents · no active lease/)).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Collapse 18 migration items' }));
    expect(within(inspector).getByRole('heading', { name: 'Implement parser.ts' })).toBeInTheDocument();
    expect(screen.getByText('Selection retained inside collapsed map')).toBeInTheDocument();
  });

  it('shows state-specific details and jumps to the failed inspector', async () => {
    const user = userEvent.setup();
    render(<WorkflowPrototype />);
    await user.click(screen.getByRole('tab', { name: 'Run graph' }));

    await user.click(screen.getByRole('button', { name: /Inspect Integration test campaign, Waiting/ }));
    let inspector = screen.getByRole('complementary', { name: 'Node details' });
    expect(within(inspector).getByText(/compilers 3\/3.*queued/)).toBeInTheDocument();
    expect(within(inspector).getByText('No artifacts produced')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Jump to failed' }));

    inspector = screen.getByRole('complementary', { name: 'Node details' });
    await waitFor(() => expect(inspector).toHaveFocus());
    expect(within(inspector).getByRole('heading', { name: 'Review parity bundler.ts' })).toBeInTheDocument();
    expect(within(inspector).getByLabelText('State: Failed')).toBeInTheDocument();
    expect(within(inspector).getByText(/mismatched types/)).toBeInTheDocument();
    expect(Element.prototype.scrollIntoView).toHaveBeenCalledWith({ block: 'nearest' });
  });

  it('keeps the graph before the inspector for narrow layouts to stack safely', async () => {
    const user = userEvent.setup();
    render(<WorkflowPrototype />);
    await user.click(screen.getByRole('tab', { name: 'Run graph' }));

    const graph = screen.getByTestId('workflow-graph-viewport');
    const inspector = screen.getByTestId('workflow-node-inspector');
    expect(graph.compareDocumentPosition(inspector) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});
