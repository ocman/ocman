// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import type { ComponentProps } from 'react';
import { ToolCallDisplay } from './ToolCallDisplay';
import { useUiStore } from '../../lib/uiStore';
import { toolApprovals } from './toolApprovals';

type Props = ComponentProps<typeof ToolCallDisplay>;
const approval = { permission: 'bash', patterns: ['pnpm test'], reasoning: 'Run the project tests.', approvedBy: 'ai' };
const renderTool = (props: Partial<Props>) => render(<ToolCallDisplay {...({
  toolName: 'bash', argsText: 'completed\n@time:10000,14500\npnpm test', result: 'passed',
  artifact: { ocmanApprovals: [approval] }, ...props,
} as Props)} />);

afterEach(() => useUiStore.setState({ compactToolCalls: false }));

describe('inline approval locks', () => {
  it.each([
    null, [], 'untrusted', {}, { ocmanApprovals: {} },
    { ocmanApprovals: [null, [], 'invalid', {}, { ...approval, patterns: [1] }, { ...approval, approvedBy: 'other' }] },
  ])('rejects invalid approval artifacts %j', artifact => {
    expect(toolApprovals(artifact)).toEqual([]);
  });

  it.each(['ai', 'user'])('places the %s approval immediately after the compact duration', approvedBy => {
    useUiStore.setState({ compactToolCalls: true });
    const { container } = renderTool({ artifact: { ocmanApprovals: [{ ...approval, approvedBy }] } });
    const lock = screen.getByRole('button', { name: `Approved by ${approvedBy === 'ai' ? 'AI' : 'user'}` });
    const heading = lock.previousElementSibling!;
    expect(heading).toHaveTextContent('$ pnpm test 4.5s');
    expect(heading.querySelector('.oc-tool-duration')).not.toBeNull();
    expect(lock.closest('.oc-tool-compact')).not.toBeNull();
    expect(lock.closest('button')?.parentElement?.closest('button')).toBeNull();
    expect(container.querySelector('.oc-tool-compact-body')).toBeNull();
    fireEvent.click(lock);
    expect(screen.getByRole('dialog', { name: 'Permission approval details' })).toHaveTextContent('Run the project tests.');
    expect(container.querySelector('.oc-tool-compact-body')).toBeNull();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('keeps the approval in the heading when output is expanded', () => {
    useUiStore.setState({ compactToolCalls: true });
    renderTool({});
    fireEvent.click(screen.getByRole('button', { name: /pnpm test/ }));
    expect(screen.getByTestId('shell-output-block')).toHaveTextContent('passed');
    expect(screen.getAllByRole('button', { name: 'Approved by AI' })).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'Approved by AI' }).closest('.oc-tool-compact-body')).toBeNull();
  });

  it('keeps long file paths below the approved heading, not before the lock', () => {
    useUiStore.setState({ compactToolCalls: true });
    renderTool({ toolName: 'edit', argsText: 'completed\n@time:10000,14500\nsrc/deeply/nested/example.ts' });
    const lock = screen.getByRole('button', { name: 'Approved by AI' });
    expect(lock.previousElementSibling).toHaveTextContent('Patching 1 file 4.5s');
    expect(lock.previousElementSibling).not.toHaveTextContent('src/deeply/nested/example.ts');
    expect(screen.getByText('src/deeply/nested/example.ts')).toBeVisible();
  });

  it('shows an unmatched approval as a lock only', () => {
    const { container } = renderTool({ toolName: 'ocman:auto-approved', argsText: '' });
    expect(screen.getByRole('button', { name: 'Approved by AI' })).toBeVisible();
    expect(container).not.toHaveTextContent('Permission approved');
    expect(container.querySelector('.oc-approval-notice')).toBeNull();
  });

  it.each([
    ['custom_tool', 'completed\n{"input":"value"}'],
    ['edit', 'completed\nsrc/example.ts'],
    ['apply_patch', 'completed\n*** Begin Patch\n*** Add File: a.ts\n+hello\n*** End Patch'],
  ])('keeps the %s lock outside the output toggle', (toolName, argsText) => {
    renderTool({ toolName, argsText });
    const lock = screen.getByRole('button', { name: 'Approved by AI' });
    const toggle = lock.previousElementSibling!.querySelector('button')!;
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(lock);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    fireEvent.mouseDown(document.body);
    expect(screen.queryByRole('dialog')).toBeNull();
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
  });
});
