// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from 'vitest';
import { act, render, fireEvent, screen } from '@testing-library/react';
import type { ComponentProps } from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
// @ts-expect-error Vitest runs in Node; application types intentionally exclude Node globals.
import { readFileSync } from 'node:fs';

const assistantThreadCss = readFileSync('src/components/AssistantThread.css', 'utf8');

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
  render(<ToolCallDisplay {...({ toolName: 'bash', ...p } as Props)} />, { wrapper: MemoryRouter });

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
    expect(header.querySelector('.oc-tool-label')?.textContent).toBe('bash (5.0s)');

    act(() => vi.advanceTimersByTime(1_000));
    expect(header.querySelector('.oc-tool-label')?.textContent).toBe('bash (6.0s)');

    rerender(<ToolCallDisplay {...({
      toolName: 'bash',
      argsText: 'completed\n@time:2000,7500\nsleep 10',
    } as Props)} />);
    expect(header.querySelector('.oc-tool-label')?.textContent).toBe('bash (5.5s)');

    act(() => vi.advanceTimersByTime(1_000));
    expect(header.querySelector('.oc-tool-label')?.textContent).toBe('bash (5.5s)');
  });

  it('cycles the running icon through braille spinner frames', () => {
    vi.useFakeTimers();
    vi.setSystemTime(7_000);
    renderTool({ argsText: 'running\n@time:2000,0\nsleep 10' });

    const spinner = screen.getByTestId('bash-spinner');
    expect(spinner.textContent).toBe('⣾');

    act(() => vi.advanceTimersByTime(80));
    expect(spinner.textContent).toBe('⣽');
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

describe('ToolCallDisplay generic tool', () => {
  it('scrolls overflowing input and output blocks when expanded', () => {
    const scrollRule = assistantThreadCss.match(/\.oc-tool-compact-body \.oc-tool-pre\s*\{[^}]*\}/)?.[0] || '';
    const { container } = renderTool({
      toolName: 'custom_tool',
      argsText: `completed\n${JSON.stringify({ input: 'line\n'.repeat(40) })}`,
      result: 'output\n'.repeat(40),
    });

    fireEvent.click(container.querySelector('.oc-tool-compact-line')!);
    const blocks = container.querySelectorAll<HTMLElement>('.oc-tool-compact-body .oc-tool-pre');
    expect(blocks).toHaveLength(2);
    expect(scrollRule).toContain('overflow: auto');
  });
});

describe('ToolCallDisplay subagent task', () => {
  const subSession = (text: string) => ({
    messages: [{ id: 'sub-m1', sessionId: 'ses_sub', timeCreated: 1, data: { role: 'assistant' } }],
    parts: [{ id: 'sub-p1', messageId: 'sub-m1', sessionId: 'ses_sub', data: { type: 'text', text } }],
  });

  it('navigates to the child session without reloading the document', () => {
    const Location = () => <span data-testid="location">{useLocation().pathname}</span>;
    render(
      <MemoryRouter>
        <ToolCallDisplay {...({
          toolName: '__task__',
          argsText: 'running\nInspect implementation',
          result: JSON.stringify({ taskId: 'ses_sub' }),
        } as Props)} />
        <Location />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole('link', { name: 'Open detailed subagent session' }));

    expect(screen.getByTestId('location').textContent).toBe('/session/ses_sub');
  });

  it.each([
    ['running', 'Running'],
    ['completed', 'Completed'],
    ['error', 'Error'],
  ])('renders %s tasks compactly by default', (status, statusTitle) => {
    const { container } = renderTool({
      toolName: '__task__',
      argsText: `${status}\nInspect implementation`,
      result: JSON.stringify({ taskId: 'ses_sub', taskOutput: '**Found** the relevant component' }),
    });

    const header = container.querySelector('.oc-tool-header')!;
    expect(header.children[0].textContent).toBe('Inspect implementation');
    expect(header.children[1].textContent).toBe(statusTitle);
    expect(screen.getByText('Found')).toBeTruthy();
    expect(container.querySelector('.oc-task-result strong')?.textContent).toBe('Found');
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

  it('expands and collapses the full markdown result by clicking the body', () => {
    const { container } = renderTool({
      toolName: '__task__',
      argsText: 'completed\nInspect implementation',
      result: JSON.stringify({ taskId: 'ses_sub', taskOutput: 'First line\n\nSecond line' }),
    });

    const sessionLink = screen.getByRole('link', { name: 'Open detailed subagent session' });
    expect(sessionLink.getAttribute('href')).toBe('/session/ses_sub');
    expect(screen.queryByRole('button', { name: 'Expand subagent task' })).toBeNull();
    const body = container.querySelector('.oc-task-result')!;
    expect(body.className).not.toContain('oc-task-result-expanded');

    fireEvent.click(body);
    expect(body.className).toContain('oc-task-result-expanded');
    fireEvent.keyDown(body, { key: 'Enter' });
    expect(body.className).not.toContain('oc-task-result-expanded');
  });

  it('limits the result preview to a few lines', () => {
    renderTool({
      toolName: '__task__',
      argsText: 'completed\nInspect implementation',
      result: JSON.stringify({ taskOutput: 'A long final summary that must not make the compact card taller on narrow screens.' }),
    });

    const resultRule = assistantThreadCss.match(/\.oc-task-result\s*\{[^}]*\}/)?.[0] || '';
    expect(resultRule).toContain('max-height: 6em');
    expect(resultRule).toContain('overflow: hidden');
  });

  it('renders only the content inside the task result XML wrapper', () => {
    const { container } = renderTool({
      toolName: '__task__',
      argsText: 'completed\nInspect implementation',
      result: JSON.stringify({ taskOutput: '  <task_result source="agent">\n**Done**\n</task_result>\nmetadata' }),
    });

    expect(container.querySelector('.oc-task-result')?.textContent).toBe('Done');
    expect(container.querySelector('.oc-task-result strong')?.textContent).toBe('Done');
  });

  it('strips task XML when truncation removed the closing tags', () => {
    const { container } = renderTool({
      toolName: '__task__',
      argsText: 'completed\nAudit auth and APIs',
      result: JSON.stringify({ taskOutput: '<task id="ses_sub" state="completed"> <task_result> ## Critical\nFinding' }),
    });

    expect(container.querySelector('.oc-task-result')?.textContent).toBe('Critical\nFinding');
    expect(container.querySelector('.oc-task-result')?.textContent).not.toContain('<task');
  });
});
