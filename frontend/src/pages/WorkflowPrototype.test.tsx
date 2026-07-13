// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { WorkflowPrototype } from './WorkflowPrototype';

describe('WorkflowPrototype', () => {
  it('switches between editable YAML, JSON, and the read-only graph', async () => {
    const user = userEvent.setup();
    render(<WorkflowPrototype />);

    const source = screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Workflow source' });
    expect(source.value).toContain('version: 7');
    await user.type(source, '\n# local edit');
    expect(screen.getByText('Edited local draft · validation not wired')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'JSON' }));
    expect(screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Workflow source' }).value).toContain('"version": 7');

    await user.click(screen.getByRole('button', { name: 'Run graph' }));
    expect(screen.queryByRole('textbox', { name: 'Workflow source' })).not.toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Workflow run graph' })).toBeInTheDocument();
  });

  it('distinguishes every workflow state with text and a symbol', async () => {
    const user = userEvent.setup();
    render(<WorkflowPrototype />);
    await user.click(screen.getByRole('button', { name: 'Run graph' }));

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
    await user.click(screen.getByRole('button', { name: 'Run graph' }));

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

    await user.click(screen.getByRole('button', { name: 'Collapse 18 migration items' }));
    expect(within(inspector).getByRole('heading', { name: 'Implement parser.ts' })).toBeInTheDocument();
    expect(screen.getByText('Selection retained inside collapsed map')).toBeInTheDocument();
  });

  it('jumps to the failed node and focuses its inspector', async () => {
    const user = userEvent.setup();
    HTMLElement.prototype.scrollIntoView = vi.fn();
    render(<WorkflowPrototype />);
    await user.click(screen.getByRole('button', { name: 'Run graph' }));

    await user.click(screen.getByRole('button', { name: 'Jump to failed' }));

    const inspector = screen.getByRole('complementary', { name: 'Node details' });
    await waitFor(() => expect(inspector).toHaveFocus());
    expect(within(inspector).getByRole('heading', { name: 'Review parity bundler.ts' })).toBeInTheDocument();
    expect(within(inspector).getByLabelText('State: Failed')).toBeInTheDocument();
  });
});
