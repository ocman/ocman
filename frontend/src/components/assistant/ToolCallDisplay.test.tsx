// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from 'vitest';
import { act, render, fireEvent, screen } from '@testing-library/react';
import type { ComponentProps } from 'react';

const printing = { isPrinting: false, collapse: false };
vi.mock('../../lib/useIsPrinting', () => ({ useIsPrinting: () => printing.isPrinting }));
vi.mock('../../lib/printCollapseContext', async (orig) => ({
  ...(await orig<typeof import('../../lib/printCollapseContext')>()),
  usePrintCollapse: () => printing.collapse,
}));

import { ToolCallDisplay } from './ToolCallDisplay';

type Props = ComponentProps<typeof ToolCallDisplay>;
const renderTool = (p: Partial<Props>) =>
  // ToolCallDisplay only reads toolName/argsText/result off the props.
  render(<ToolCallDisplay {...({ toolName: 'bash', ...p } as Props)} />);

afterEach(() => vi.useRealTimers());

describe('ToolCallDisplay bash collapse', () => {
  it('collapses the whole body to just the header on click', () => {
    const { container } = renderTool({
      argsText: JSON.stringify({ command: 'echo hi' }),
      result: 'hi',
    });
    const tool = container.querySelector('.oc-tool-shell')!;
    // Open by default: the output block is present.
    expect(tool.querySelector('[data-testid="shell-output-block"]')).not.toBeNull();
    // Clicking the header collapses: no output at all, header remains.
    fireEvent.click(tool.querySelector('.oc-tool-header')!);
    expect(tool.querySelector('[data-testid="shell-output-block"]')).toBeNull();
    expect(tool.querySelector('.oc-tool-label')).not.toBeNull();
    // Clicking again re-expands.
    fireEvent.click(tool.querySelector('.oc-tool-header')!);
    expect(tool.querySelector('[data-testid="shell-output-block"]')).not.toBeNull();
  });

  it('hides the body while printing with collapse-tools on', () => {
    printing.isPrinting = true;
    printing.collapse = true;
    try {
      const { container } = renderTool({
        argsText: JSON.stringify({ command: 'echo hi' }),
        result: 'hi',
      });
      const tool = container.querySelector('.oc-tool-shell')!;
      expect(tool.querySelector('[data-testid="shell-output-block"]')).toBeNull();
      expect(tool.querySelector('.oc-tool-label')).not.toBeNull();
    } finally {
      printing.isPrinting = false;
      printing.collapse = false;
    }
  });

  it('previews 12 lines and shows the full output on request', () => {
    const output = Array.from({ length: 13 }, (_, i) => `line ${i + 1}`).join('\n');
    renderTool({
      argsText: JSON.stringify({ command: 'many-lines' }),
      result: output,
    });

    expect(screen.getByTestId('shell-output-block').textContent).toContain('line 12');
    expect(screen.getByTestId('shell-output-block').textContent).not.toContain('line 13');
    fireEvent.click(screen.getByRole('button', { name: 'Show full output' }));
    expect(screen.getByTestId('shell-output-block').textContent).toContain('line 13');
  });

  it('previews 5000 characters and shows the full output on request', () => {
    const output = `${'a'.repeat(5000)}LAST`;
    renderTool({
      argsText: JSON.stringify({ command: 'many-characters' }),
      result: output,
    });

    expect(screen.getByTestId('shell-output-block').textContent).not.toContain('LAST');
    fireEvent.click(screen.getByRole('button', { name: 'Show full output' }));
    expect(screen.getByTestId('shell-output-block').textContent).toContain('LAST');
  });
});

describe('ToolCallDisplay bash duration', () => {
  it('ticks in the header while running and freezes when completed', () => {
    vi.useFakeTimers();
    vi.setSystemTime(7_000);
    const { container, rerender } = renderTool({
      argsText: 'running\n@time:2000,0\nsleep 10',
    });

    const header = container.querySelector('.oc-tool-header')!;
    expect(header.querySelector('.oc-tool-duration')?.textContent).toBe('5.0s');

    act(() => vi.advanceTimersByTime(1_000));
    expect(header.querySelector('.oc-tool-duration')?.textContent).toBe('6.0s');

    rerender(<ToolCallDisplay {...({
      toolName: 'bash',
      argsText: 'completed\n@time:2000,7500\nsleep 10',
    } as Props)} />);
    expect(header.querySelector('.oc-tool-duration')?.textContent).toBe('5.5s');

    act(() => vi.advanceTimersByTime(1_000));
    expect(header.querySelector('.oc-tool-duration')?.textContent).toBe('5.5s');
  });
});

describe('ToolCallDisplay auto-approved notice', () => {
  it('renders permission, patterns and reasoning each on their own line', () => {
    const { container, getByTestId } = renderTool({
      toolName: 'ocman:auto-approved',
      argsText: JSON.stringify({
        permission: 'external_directory',
        patterns: ['/tmp/foo', '/tmp/bar'],
        reasoning: 'Reads a temp file, no write access.',
      }),
    });
    expect(container.querySelector('.oc-auto-approved-action')!.textContent).toBe(
      'external_directory',
    );
    expect(container.querySelector('.oc-auto-approved-patterns')!.textContent).toBe(
      '/tmp/foo, /tmp/bar',
    );
    expect(getByTestId('auto-approved-reasoning').textContent).toBe(
      'Reads a temp file, no write access.',
    );
  });

  it('omits absent fields', () => {
    const { container, queryByTestId } = renderTool({
      toolName: 'ocman:auto-approved',
      argsText: JSON.stringify({ permission: 'external_directory' }),
    });
    expect(container.querySelector('.oc-auto-approved-patterns')).toBeNull();
    expect(queryByTestId('auto-approved-reasoning')).toBeNull();
  });
});

describe('ToolCallDisplay subagent task', () => {
  const subSession = (text: string) => ({
    messages: [{ id: 'sub-m1', sessionId: 'ses_sub', timeCreated: 1, data: { role: 'assistant' } }],
    parts: [{ id: 'sub-p1', messageId: 'sub-m1', sessionId: 'ses_sub', data: { type: 'text', text } }],
  });

  it.each([
    ['running', 'Running'],
    ['completed', 'Completed'],
    ['error', 'Error'],
  ])('renders %s tasks compactly by default', (status, statusTitle) => {
    renderTool({
      toolName: '__task__',
      argsText: `${status}\nInspect implementation`,
      result: JSON.stringify({ taskId: 'ses_sub', subSession: subSession('Found the relevant component') }),
    });

    expect(screen.getByTitle(statusTitle)).toBeTruthy();
    expect(screen.getByText('Inspect implementation')).toBeTruthy();
    expect(screen.getByText('Found the relevant component')).toBeTruthy();
    expect(screen.queryByTestId('embedded-thread')).toBeNull();
  });

  it('updates the latest activity and falls back when none is available', () => {
    const { rerender } = renderTool({
      toolName: '__task__',
      argsText: 'running\nInspect implementation',
      result: JSON.stringify({ taskId: 'ses_sub' }),
    });
    expect(screen.getByText('Waiting for activity...')).toBeTruthy();

    rerender(<ToolCallDisplay {...({
      toolName: '__task__',
      argsText: 'running\nInspect implementation',
      result: JSON.stringify({
        taskId: 'ses_sub',
        subSession: subSession('Found the first file'),
        liveTools: [{ toolName: 'Grep', summary: 'Searching for consumers' }],
      }),
    } as Props)} />);
    expect(screen.getByText('Grep: Searching for consumers')).toBeTruthy();
    expect(screen.queryByText('Waiting for activity...')).toBeNull();
  });

  it('expands and collapses the thread independently from session navigation', () => {
    renderTool({
      toolName: '__task__',
      argsText: 'completed\nInspect implementation',
      result: JSON.stringify({ taskId: 'ses_sub', subSession: subSession('Full subagent output') }),
    });

    const sessionLink = screen.getByRole('link', { name: 'Open detailed subagent session' });
    expect(sessionLink.getAttribute('href')).toBe('/session/ses_sub');
    expect(screen.queryByTestId('embedded-thread')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Expand subagent task' }));
    expect(screen.getByTestId('embedded-thread')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Collapse subagent task' }));
    expect(screen.queryByTestId('embedded-thread')).toBeNull();
  });

  it('uses a one-line summary hook for narrow layouts', () => {
    renderTool({
      toolName: '__task__',
      argsText: 'completed\nInspect implementation',
      result: JSON.stringify({ taskOutput: 'A long final summary that must not make the compact card taller on narrow screens.' }),
    });

    expect(screen.getByTestId('subagent-activity').className).toContain('oc-task-activity');
  });
});
