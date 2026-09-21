// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ReactFlow } from '@xyflow/react';
import type { FactoryIssue } from '../lib/api';
import { EpicGraph } from './EpicGraph';

vi.mock('@xyflow/react', async (importOriginal) => ({
  ...await importOriginal<typeof import('@xyflow/react')>(),
  ReactFlow: vi.fn(() => null),
}));

vi.mock('./FactoryIssues', () => ({
  IssueDrawer: ({ issue, onClose }: { issue: FactoryIssue; onClose: () => void }) => <button onClick={onClose}>Close {issue.title}</button>,
}));

describe('workflow graph', () => {
  it('opens implementation task details and keeps proposal previews read-only', () => {
    const issues: FactoryIssue[] = [{ id: 'task', kind: 'implementation', epicId: 'epic', project: '/repo', title: 'Task', status: 'open' }];
    const { rerender } = render(<EpicGraph issues={issues} />);
    const props = vi.mocked(ReactFlow).mock.calls.at(-1)![0];
    act(() => props.onNodeClick!({} as never, props.nodes![0]));
    fireEvent.click(screen.getByRole('button', { name: 'Close Task' }));
    expect(screen.queryByRole('button', { name: 'Close Task' })).not.toBeInTheDocument();
    rerender(<EpicGraph issues={issues} preview />);
    expect(vi.mocked(ReactFlow).mock.calls.at(-1)![0].onNodeClick).toBeUndefined();
  });

  it('shows every workflow step, implementation task and dependency without expanding phases', () => {
    const issue = (id: string, kind: string, parentId?: string, blockers: string[] = []): FactoryIssue => ({
      id, kind, parentId, epicId: 'epic', project: '/repo', title: id, status: 'open',
      dependsOn: blockers.map((id) => ({ id, type: 'blocks' })),
    });
    const issues = [
      issue('plan', 'plan'),
      issue('approve', 'gate', undefined, ['plan']),
      issue('implement', 'phase', undefined, ['approve']),
      issue('thread-session', 'implementation', 'implement', ['approve']),
      issue('continuity', 'implementation', 'implement', ['thread-session']),
      issue('verify', 'task', undefined, ['implement']),
      issue('deliver', 'delivery', undefined, ['verify']),
    ];
    const { rerender } = render(<EpicGraph />);
    expect(screen.getByText('This epic has no work to draw yet.')).toBeInTheDocument();
    rerender(<EpicGraph issues={issues} />);
    const props = vi.mocked(ReactFlow).mock.calls.at(-1)![0];
    expect(props.nodes?.map((node) => node.id)).toEqual(issues.map((issue) => issue.id));
    expect(props.edges).toEqual(expect.arrayContaining([
      expect.objectContaining({ source: 'plan', target: 'approve' }),
      expect.objectContaining({ source: 'approve', target: 'implement' }),
      expect.objectContaining({ source: 'approve', target: 'thread-session' }),
      expect.objectContaining({ source: 'thread-session', target: 'continuity' }),
      expect.objectContaining({ source: 'implement', target: 'verify' }),
      expect.objectContaining({ source: 'verify', target: 'deliver' }),
    ]));
    rerender(<EpicGraph issues={[...issues, issue('new-task', 'implementation', 'implement', ['continuity'])]} />);
    const updated = vi.mocked(ReactFlow).mock.calls.at(-1)![0];
    expect(updated.nodes?.map((node) => node.id)).toContain('new-task');
    expect(updated.edges).toContainEqual(expect.objectContaining({ source: 'continuity', target: 'new-task' }));
  });
});
