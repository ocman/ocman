// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { ReactFlow } from '@xyflow/react';
import { expect, it, vi } from 'vitest';
import type { FactoryIssue } from '../lib/api';
import { FactoryPlanGraph } from './FactoryPlanGraph';

vi.mock('@xyflow/react', async (importOriginal) => ({
  ...await importOriginal<typeof import('@xyflow/react')>(),
  ReactFlow: vi.fn(() => null),
}));
vi.mock('../pages/FactoryIssues', () => ({
  IssueDrawer: ({ issue, preview, onClose }: { issue: FactoryIssue; preview?: boolean; onClose: () => void }) => <button onClick={onClose}>{preview ? 'Preview' : 'Issue'} {issue.description}</button>,
}));

const issues: FactoryIssue[] = [{ id: 'task', kind: 'implementation', epicId: 'epic', project: '/repo', title: 'Task', description: 'Task details', status: 'open' }];

it.each(['click', 'Enter', ' '])('inspects inline plan nodes using %s', (gesture) => {
  render(<FactoryPlanGraph issues={issues} />);
  const node = screen.getByRole('button', { name: 'Inspect Task' });
  if (gesture === 'click') fireEvent.click(node);
  else fireEvent.keyDown(node, { key: gesture });
  fireEvent.click(screen.getByRole('button', { name: 'Preview Task details' }));
  expect(screen.queryByRole('button', { name: 'Preview Task details' })).not.toBeInTheDocument();
});

it('inspects expanded conversation plan nodes and keeps the graph open on drawer close', () => {
  render(<FactoryPlanGraph issues={issues} />);
  fireEvent.click(screen.getByRole('button', { name: 'Expand plan graph' }));
  const props = vi.mocked(ReactFlow).mock.calls.at(-1)![0];
  act(() => props.onNodeClick!({} as never, props.nodes![0]));
  fireEvent.click(screen.getByRole('button', { name: 'Preview Task details' }));
  expect(screen.getByRole('dialog', { name: 'Plan graph' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Close plan graph' }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('omits empty plan graphs', () => {
  const { container } = render(<FactoryPlanGraph issues={[]} />);
  expect(container).toBeEmptyDOMElement();
});
