// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import type { ComponentProps } from 'react';
import { ToolCallDisplay } from './ToolCallDisplay';
import { useUiStore } from '../../lib/uiStore';

type Props = ComponentProps<typeof ToolCallDisplay>;

afterEach(() => {
  useUiStore.setState({ compactToolCalls: false });
  vi.useRealTimers();
});

describe('compact shell descriptions', () => {
  it('counts elapsed time after the description and freezes at end minus start', () => {
    vi.useFakeTimers();
    vi.setSystemTime(12000);
    useUiStore.setState({ compactToolCalls: true });
    const props = { toolName: 'bash', argsText: 'running\n@time:10000,0\n@desc:Run tests\npnpm test' } as Props;
    const { rerender } = render(<ToolCallDisplay {...props} />);
    expect(screen.getByRole('button', { name: /Bash: Run tests 2.0s/ })).toBeVisible();
    act(() => vi.advanceTimersByTime(3000));
    expect(screen.getByRole('button', { name: /Bash: Run tests 5.0s/ })).toBeVisible();
    rerender(<ToolCallDisplay {...props} argsText={'completed\n@time:10000,14500\n@desc:Run tests\npnpm test'} />);
    expect(screen.getByRole('button', { name: /Bash: Run tests 4.5s/ })).toBeVisible();
    act(() => vi.advanceTimersByTime(3000));
    expect(screen.getByRole('button', { name: /Bash: Run tests 4.5s/ })).toBeVisible();
  });

  it.each(['bash', 'mcp_bash', 'mcp_Bash'])('shows the description above the %s command', (toolName) => {
    useUiStore.setState({ compactToolCalls: true });
    render(<ToolCallDisplay {...({
      toolName, argsText: 'completed\n@desc:Run frontend tests\npnpm test', result: 'passed',
    } as Props)} />);
    const row = screen.getByRole('button', { name: /Bash: Run frontend tests/ });
    expect(row).toHaveTextContent('→Bash: Run frontend tests');
    expect(row).toHaveTextContent('$ pnpm test');
    fireEvent.click(row);
    expect(screen.getByText('passed')).toBeVisible();
  });

  it('keeps the command when no description is supplied', () => {
    useUiStore.setState({ compactToolCalls: true });
    render(<ToolCallDisplay {...({ toolName: 'bash', argsText: 'completed\npnpm test', result: 'passed' } as Props)} />);
    expect(screen.getByRole('button', { name: '$ pnpm test' })).toHaveTextContent('→$ pnpm test');
  });

  it('updates the description indicator as shell status changes', () => {
    useUiStore.setState({ compactToolCalls: true });
    const props = { toolName: 'bash', argsText: 'running\n@desc:Run tests\npnpm test' } as Props;
    const { container, rerender } = render(<ToolCallDisplay {...props} />);
    expect(screen.getByRole('button', { name: /Bash: Run tests/ })).toBeVisible();
    expect(screen.getByTestId('bash-spinner')).toBeVisible();
    expect(screen.getByTitle('Running')).toBeInTheDocument();
    expect(container.querySelector('.oc-tool-compact-running')).not.toBeNull();
    rerender(<ToolCallDisplay {...props} argsText={'completed\n@desc:Run tests\npnpm test'} result="passed" />);
    expect(screen.getByTitle('Completed')).toBeInTheDocument();
    expect(screen.queryByTestId('bash-spinner')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Bash: Run tests/ })).toHaveTextContent('$ pnpm test');
    expect(container.querySelector('.oc-tool-compact-running')).toBeNull();
  });
});

describe('compact file changes', () => {
  it.each(['edit', 'mcp_edit', 'mcp_Edit', 'write', 'mcp_write', 'mcp_Write'])(
    'renders %s as a muted row and reveals its result on click', (toolName) => {
      useUiStore.setState({ compactToolCalls: true });
      const { container } = render(<ToolCallDisplay {...({
        toolName, argsText: 'completed\nsrc/example.ts', result: 'File updated successfully',
      } as Props)} />);

      const row = screen.getByRole('button', { name: /(?:Writing|Patching) 1 file.*src\/example.ts/ });
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
    expect(screen.getByRole('button', { name: /Patching 1 file.*src\/new.ts/ })).toHaveClass('oc-tool-compact-line');
    expect(container.querySelector('.oc-tool-patch')).toBeNull();
    fireEvent.click(screen.getByRole('button'));
    expect(container.querySelector('.oc-tool-patch')).not.toBeNull();
  });

  it('lists every patch path relative to the project, without patch contents', () => {
    useUiStore.setState({ compactToolCalls: true });
    render(<ToolCallDisplay {...({
      toolName: 'apply_patch',
      argsText: 'completed\n*** Begin Patch\n*** Update File: /repo/relative/path.js\n@@\n-old\n+new\n*** Add File: /repo/other/file.go\n+package main\n*** End Patch',
      artifact: { projectDirectory: '/repo' }, result: 'Patch applied',
    } as Props)} />);
    expect(screen.getByRole('button', { name: /Patching 2 files.*relative\/path.js.*other\/file.go/ })).toBeVisible();
    expect(screen.getByText('relative/path.js', { exact: true })).toBeVisible();
    expect(screen.getByText('other/file.go', { exact: true })).toBeVisible();
    expect(screen.queryByText('package main')).not.toBeInTheDocument();
  });

  it('does not repeat the Write prefix from converted tool arguments', () => {
    useUiStore.setState({ compactToolCalls: true });
    render(<ToolCallDisplay {...({ toolName: 'write', argsText: 'completed\nWrite src/new.ts', result: 'Written' } as Props)} />);
    expect(screen.getByRole('button', { name: /Writing 1 file.*src\/new.ts/ })).toBeVisible();
    expect(screen.getByText('src/new.ts', { exact: true })).toBeVisible();
  });

  it.each(['filePath', 'file_path', 'path'])('reads JSON %s paths without displaying file contents', (key) => {
    useUiStore.setState({ compactToolCalls: true });
    render(<ToolCallDisplay {...({ toolName: 'write', argsText: `completed\n${JSON.stringify({ [key]: '/repo/src/new.ts', content: 'secret contents' })}`, artifact: { projectDirectory: '/repo' } } as Props)} />);
    expect(screen.getByText('src/new.ts', { exact: true })).toBeVisible();
    expect(screen.queryByText(/secret contents/)).not.toBeInTheDocument();
  });

  it('deduplicates repeated patch paths', () => {
    useUiStore.setState({ compactToolCalls: true });
    render(<ToolCallDisplay {...({ toolName: 'apply_patch', argsText: 'completed\n*** Begin Patch\n*** Update File: src/a.ts\n@@\n-a\n+b\n*** Update File: src/a.ts\n@@\n-c\n+d\n*** End Patch' } as Props)} />);
    expect(screen.getByRole('button', { name: /Patching 1 file.*src\/a.ts/ })).toBeVisible();
    expect(screen.getAllByText('src/a.ts', { exact: true })).toHaveLength(1);
  });

  it.each(['write', 'apply_patch'])('does not invent a file count for empty %s arguments', toolName => {
    useUiStore.setState({ compactToolCalls: true });
    render(<ToolCallDisplay {...({ toolName, argsText: 'completed\n' } as Props)} />);
    expect(screen.getByRole('button', { name: /(?:Writing|Patching) files/ })).toBeVisible();
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
