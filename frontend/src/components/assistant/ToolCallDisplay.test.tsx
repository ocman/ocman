// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { render, fireEvent } from '@testing-library/react';
import type { ComponentProps } from 'react';
import { ToolCallDisplay } from './ToolCallDisplay';

type Props = ComponentProps<typeof ToolCallDisplay>;
const renderTool = (p: Partial<Props>) =>
  // ToolCallDisplay only reads toolName/argsText/result off the props.
  render(<ToolCallDisplay {...({ toolName: 'bash', ...p } as Props)} />);

const longOutput = Array.from({ length: 40 }, (_, i) => `line ${i}`).join('\n');

describe('ToolCallDisplay bash collapse', () => {
  it('toggles the expanded class for long output', () => {
    const { container } = renderTool({
      argsText: JSON.stringify({ command: 'ls' }),
      result: longOutput,
    });
    const tool = container.querySelector('.oc-tool-shell')!;
    // Long output starts collapsed (no oc-tool-expanded).
    expect(tool.classList.contains('oc-tool-expanded')).toBe(false);
    fireEvent.click(tool.querySelector('.oc-tool-header')!);
    expect(tool.classList.contains('oc-tool-expanded')).toBe(true);
    // Header click collapses again — this is the regression: it must flip back.
    fireEvent.click(tool.querySelector('.oc-tool-header')!);
    expect(tool.classList.contains('oc-tool-expanded')).toBe(false);
  });

  it('does not attach a dead toggle for short output', () => {
    const { container } = renderTool({
      argsText: JSON.stringify({ command: 'echo hi' }),
      result: 'hi',
    });
    const header = container.querySelector('.oc-tool-header') as HTMLElement;
    // Short output is fully shown; the header must not look clickable.
    expect(header.style.cursor).toBe('');
  });
});
