// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { FactoryIssue } from '../lib/api';
import { EpicGraph } from './EpicGraph';

vi.mock('./FactoryIssues', () => ({
  IssueDrawer: ({ issue, onClose }: { issue: FactoryIssue; onClose: () => void }) => <button onClick={onClose}>Close {issue.title}</button>,
}));

const nodeIDs = () => screen.getAllByTestId(/^epic-graph-node-/).map((node) => node.dataset.testid!.replace('epic-graph-node-', ''));
const edge = (id: string) => screen.getByTestId(`epic-graph-edge-${id}`);

describe('workflow graph', () => {
  it.each([false, true])('opens task details with preview=%s', (preview) => {
    const issues: FactoryIssue[] = [{ id: 'task', kind: 'implementation', epicId: 'epic', project: '/repo', title: 'Task', status: 'open' }];
    render(<EpicGraph issues={issues} preview={preview} />);
    fireEvent.click(screen.getByRole('button', { name: 'Inspect Task' }));
    fireEvent.click(screen.getByRole('button', { name: 'Close Task' }));
    expect(screen.queryByRole('button', { name: 'Close Task' })).not.toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole('button', { name: 'Inspect Task' }), { key: 'Enter' });
    expect(screen.getByRole('button', { name: 'Close Task' })).toBeInTheDocument();
  });

  it('offers zoom and fit controls', () => {
    render(<EpicGraph issues={[{ id: 'task', kind: 'implementation', epicId: 'epic', project: '/repo', title: 'Task', status: 'open' }]} />);
    for (const name of ['Zoom in', 'Zoom out', 'Fit view']) fireEvent.click(within(screen.getByTestId('epic-graph')).getByRole('button', { name }));
    expect(screen.getByRole('button', { name: 'Inspect Task' })).toBeInTheDocument();
  });

  it('draws decision provenance without a dependency arrow or implementation placeholder', () => {
    render(<EpicGraph issues={[
      { id: 'phase', kind: 'phase', epicId: 'epic', project: '/repo', title: 'Implement', status: 'open', dispatchState: 'ready' },
      { id: 'task', kind: 'implementation', parentId: 'phase', requirement: 'required', epicId: 'epic', project: '/repo', title: 'Task', status: 'in_progress' },
      { id: 'gate', kind: 'gate', parentId: 'phase', requirement: 'reference', epicId: 'epic', project: '/repo', title: 'Permission', status: 'closed', outcome: 'succeeded', authority: { issueId: 'gate', epicId: 'epic', attemptId: 'a', workId: 'task', requestId: 'r', permission: 'bash', target: 'test', resolution: 'approve' } },
    ]} />);
    const provenance = edge('interrupts:task->gate');
    expect(provenance).toHaveTextContent('decision for task');
    expect(provenance.querySelector('path')).not.toHaveAttribute('marker-end');
    expect(nodeIDs()).not.toContain('phase');
    expect(within(screen.getByTestId('epic-graph-node-gate')).getByText('permission decision · done')).toBeInTheDocument();
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
    expect(nodeIDs()).toEqual(issues.filter((issue) => issue.kind !== 'phase').map((issue) => issue.id));
    for (const id of ['plan->approve', 'approve->thread-session', 'thread-session->continuity', 'continuity->verify', 'verify->deliver']) {
      expect(edge(`blocks:${id}`)).toBeInTheDocument();
    }
    rerender(<EpicGraph issues={[...issues, issue('new-task', 'implementation', 'implement', ['continuity'])]} />);
    expect(nodeIDs()).toContain('new-task');
    expect(edge('blocks:continuity->new-task')).toBeInTheDocument();
    expect(edge('blocks:new-task->verify')).toBeInTheDocument();
  });

  it('labels containment, phase completion and failure paths', () => {
    const issue = (id: string, kind: string, extra: Partial<FactoryIssue> = {}): FactoryIssue => ({ id, kind, epicId: 'epic', project: '/repo', title: id, status: 'open', ...extra });
    const { rerender } = render(<EpicGraph issues={[issue('group', 'mol'), issue('child', 'task', { parentId: 'group' })]} preview />);
    expect(edge('hierarchy:group->child')).toHaveTextContent('contains');
    expect(edge('hierarchy:group->child').querySelector('path')).not.toHaveAttribute('marker-end');
    rerender(<EpicGraph issues={[
      issue('phase', 'phase'),
      issue('check', 'gate', { parentId: 'phase' }),
      issue('repair', 'task', { dependsOn: [{ id: 'check', type: 'on_failure' }] }),
    ]} />);
    expect(edge('completion:check->phase')).toHaveTextContent('phase completion');
    expect(edge('on_failure:check->repair')).toHaveTextContent('on failure');
  });

  it('keeps dependency and merge-gate lines static', () => {
    const { container } = render(<EpicGraph issues={[
      { id: 'first', kind: 'delivery', epicId: 'epic', project: '/repo', title: 'First', status: 'open' },
      { id: 'next', kind: 'implementation', epicId: 'epic', project: '/other', title: 'Next', status: 'open', dependsOn: [{ id: 'first', type: 'blocks' }, { id: 'first', type: 'merge_gated' }] },
    ]} />);
    expect(container.querySelectorAll('.factory-edge')).toHaveLength(1);
    const line = edge('blocks:first->next');
    expect(line).toHaveTextContent('blocks · merge gated');
    expect(line.querySelector('path')).toHaveAttribute('marker-end');
  });
});
