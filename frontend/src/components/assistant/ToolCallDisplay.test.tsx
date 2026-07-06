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
