// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/react';
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
