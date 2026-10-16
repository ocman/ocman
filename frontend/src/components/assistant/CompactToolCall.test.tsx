// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import type { ComponentProps } from 'react';
import { ToolCallDisplay } from './ToolCallDisplay';
import { useUiStore } from '../../lib/uiStore';

type Props = ComponentProps<typeof ToolCallDisplay>;

afterEach(() => useUiStore.setState({ compactToolCalls: false }));

describe('compact file changes', () => {
  it.each(['edit', 'mcp_edit', 'mcp_Edit', 'write', 'mcp_write', 'mcp_Write'])(
    'renders %s as a muted row and reveals its result on click', (toolName) => {
      useUiStore.setState({ compactToolCalls: true });
      const { container } = render(<ToolCallDisplay {...({
        toolName, argsText: 'completed\nsrc/example.ts', result: 'File updated successfully',
      } as Props)} />);

      const row = screen.getByRole('button', { name: /src\/example.ts/ });
      expect(row).toHaveClass('oc-tool-compact-line');
      expect(container.querySelector('.oc-tool-edit')).toBeNull();
      expect(screen.queryByText('File updated successfully')).not.toBeInTheDocument();
      fireEvent.click(row);
      expect(screen.getByText('File updated successfully')).toBeInTheDocument();
      fireEvent.click(row);
      expect(screen.queryByText('File updated successfully')).not.toBeInTheDocument();
    },
  );

  it('keeps structured diffs collapsed', () => {
    useUiStore.setState({ compactToolCalls: true });
    const { container } = render(<ToolCallDisplay {...({
      toolName: 'edit', argsText: 'completed\nsrc/example.ts',
      result: JSON.stringify({ __diff: true, filePath: 'src/example.ts', before: 'old', after: 'new' }),
    } as Props)} />);
    expect(screen.getByRole('button', { name: /src\/example.ts/ })).toHaveAttribute('aria-expanded', 'false');
    expect(container.querySelector('.oc-tool-content')).toBeNull();
  });

  it('summarizes patch files without showing the patch block', () => {
    useUiStore.setState({ compactToolCalls: true });
    const { container } = render(<ToolCallDisplay {...({
      toolName: 'apply_patch',
      argsText: 'completed\n*** Begin Patch\n*** Add File: src/new.ts\n+hello\n*** End Patch',
      result: 'Patch applied',
    } as Props)} />);
    expect(screen.getByRole('button', { name: /src\/new.ts/ })).toHaveClass('oc-tool-compact-line');
    expect(container.querySelector('.oc-tool-patch')).toBeNull();
    fireEvent.click(screen.getByRole('button'));
    expect(container.querySelector('.oc-tool-patch')).not.toBeNull();
  });

  it('preserves the error indicator on muted file changes', () => {
    useUiStore.setState({ compactToolCalls: true });
    const { container } = render(<ToolCallDisplay {...({
      toolName: 'write', argsText: 'error\nsrc/example.ts', result: 'Write failed',
    } as Props)} />);
    expect(screen.getByTitle('Error')).toBeInTheDocument();
    expect(container.querySelector('.oc-tool-compact-error')).not.toBeNull();
  });
});
