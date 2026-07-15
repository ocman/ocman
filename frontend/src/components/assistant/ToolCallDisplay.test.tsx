// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from 'vitest';
import { act, render, fireEvent } from '@testing-library/react';
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
