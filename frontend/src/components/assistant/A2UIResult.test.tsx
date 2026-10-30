// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { A2UIResult, A2UIToolResult } from './A2UIResult';
import { ToolCallDisplay } from './ToolCallDisplay';
import { A2UIActionContext } from '../../lib/a2uiActionContext';
import { sampleResult } from '../../test/a2uiFixture';
import type { ToolCallMessagePartProps } from '@assistant-ui/react';

describe('A2UI result rendering', () => {
  it('renders text, layouts, accessible charts and sortable numeric tables without executing HTML', () => {
    const ui = sampleResult();
    ui.summary = '<script>alert(1)</script>';
    const { container } = render(<A2UIResult result={ui} />);
    expect(screen.getByRole('region', { name: 'Build investigation' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Failed jobs' })).toBeInTheDocument();
    expect(screen.getByText('Failures by job')).toBeInTheDocument();
    expect(screen.getByText('<script>alert(1)</script>')).toBeInTheDocument();
    expect(container.querySelector('script')).toBeNull();
    expect(container.querySelector('hr')).toBeInTheDocument();
    const table = screen.getByRole('table');
    const failures = within(table).getByRole('button', { name: 'Failures' });
    expect(failures).toHaveAttribute('title', 'Sort ascending');
    expect(within(failures).getByText('↕')).toHaveAttribute('aria-hidden', 'true');
    fireEvent.click(within(table).getByRole('button', { name: 'Failures' }));
    expect(failures).toHaveAttribute('title', 'Sort descending');
    expect(within(failures).getByText('↑')).toHaveAttribute('aria-hidden', 'true');
    expect(within(table).getAllByRole('row')[1]).toHaveTextContent('Backend2');
    fireEvent.click(within(table).getByRole('button', { name: 'Failures' }));
    expect(failures).toHaveAttribute('title', 'Sort ascending');
    expect(within(failures).getByText('↓')).toHaveAttribute('aria-hidden', 'true');
    expect(within(table).getAllByRole('row')[1]).toHaveTextContent('Frontend10');
    fireEvent.click(within(table).getByRole('button', { name: 'Job' }));
    expect(within(table).getAllByRole('row')[1]).toHaveTextContent('Backend2');
  });

  it('is read-only without an owner-scoped callback', () => {
    render(<A2UIResult result={sampleResult()} />);
    expect(screen.getByRole('textbox', { name: 'Investigate job' })).toBeDisabled();
    expect(screen.getByRole('checkbox', { name: 'Include logs' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Continue investigation' })).toBeDisabled();
    expect(screen.getByText(/Read-only/)).toBeInTheDocument();
  });

  it('binds form values locally and confirms the resolved action before sending', async () => {
    const send = vi.fn().mockResolvedValue(undefined);
    render(<A2UIActionContext.Provider value={{ scope: 'owner:session', send }}><A2UIResult result={sampleResult()} /></A2UIActionContext.Provider>);
    fireEvent.change(screen.getByRole('textbox', { name: 'Investigate job' }), { target: { value: 'Frontend' } });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Include logs' }));
    expect(send).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Continue investigation' }));
    expect(screen.getByRole('dialog')).toHaveTextContent('Frontend');
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus();
    expect(send).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Send response' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(send).toHaveBeenCalledExactlyOnceWith({ action: {
      name: 'investigate', surfaceId: 'build', sourceComponentId: 'button', timestamp: expect.any(String),
      context: { job: 'Frontend', logs: true, fixed: 'literal', missing: null },
    } });
    expect(screen.getByRole('status')).toHaveTextContent('Response sent');
  });

  it('cancels without sending and preserves values for retry on failure', async () => {
    const send = vi.fn().mockRejectedValueOnce(new Error('Owner unavailable')).mockResolvedValue(undefined);
    render(<A2UIActionContext.Provider value={{ scope: 'owner:session', send }}><A2UIResult result={sampleResult()} /></A2UIActionContext.Provider>);
    fireEvent.click(screen.getByRole('button', { name: 'Continue investigation' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(send).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Continue investigation' }));
    fireEvent.click(screen.getByRole('button', { name: 'Send response' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Owner unavailable');
    expect(screen.getByRole('dialog')).toHaveTextContent('Backend');
    fireEvent.click(screen.getByRole('button', { name: 'Send response' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(send).toHaveBeenCalledTimes(2);
  });

  it('prevents duplicate sends and disables all modal dismissal while sending', async () => {
    let finish!: () => void;
    const send = vi.fn(() => new Promise<void>(resolve => { finish = resolve; }));
    render(<A2UIActionContext.Provider value={{ scope: 'owner:session', send }}><A2UIResult result={sampleResult()} /></A2UIActionContext.Provider>);
    fireEvent.click(screen.getByRole('button', { name: 'Continue investigation' }));
    fireEvent.click(screen.getByRole('button', { name: 'Send response' }));
    expect(screen.getByRole('button', { name: 'Sending…' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Close response review' })).toBeDisabled();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    finish();
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(send).toHaveBeenCalledTimes(1);
  });

  it('clears unsent interactions when switching sessions', () => {
    const send = vi.fn();
    const result = JSON.stringify({ ocmanA2ui: sampleResult() });
    const view = (scope: string) => <A2UIActionContext.Provider value={{ scope, send }}><A2UIToolResult toolName="ocman_render_ui" argsText={'completed\n{}'} result={result} /></A2UIActionContext.Provider>;
    const { rerender } = render(view('owner:first'));
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'unsent' } });
    fireEvent.click(screen.getByRole('button', { name: 'Continue investigation' }));
    rerender(view('owner:second'));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('textbox')).toHaveValue('Backend');
    expect(send).not.toHaveBeenCalled();
  });

  it('renders beneath a completed tool call and survives reload from retained JSON', () => {
    const props = { toolName: 'ocman_render_ui', toolCallId: 'call', argsText: 'completed\n{}', result: JSON.stringify({ ocmanA2ui: sampleResult() }) } as ToolCallMessagePartProps;
    const { unmount } = render(<ToolCallDisplay {...props} />, { wrapper: MemoryRouter });
    expect(screen.getByRole('region', { name: 'Build investigation' })).toBeInTheDocument();
    unmount();
    render(<ToolCallDisplay {...props} />, { wrapper: MemoryRouter });
    expect(screen.getByRole('table')).toBeInTheDocument();
  });

  it('leaves invalid UI as an ordinary expandable tool result', () => {
    const props = { toolName: 'ocman_render_ui', argsText: 'completed\n{}', result: '{"ocmanA2ui":{"title":"Invalid"}}' } as ToolCallMessagePartProps;
    render(<ToolCallDisplay {...props} />, { wrapper: MemoryRouter });
    expect(screen.queryByRole('region')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button'));
    expect(screen.getByText(props.result as string)).toBeInTheDocument();
  });
});
