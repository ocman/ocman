import { describe, expect, it } from 'vitest';
import { factoryGraphModel, factoryIssueState } from './factoryGraph';
import type { FactoryIssue } from '../lib/api';

const issue = (overrides: Partial<FactoryIssue> & Pick<FactoryIssue, 'id'>): FactoryIssue => ({
  epicId: 'epic-1', kind: 'task', title: 'Work', status: 'open', ...overrides,
});

describe('factoryIssueState', () => {
  it.each([
    ['done', issue({ id: 'a', status: 'closed', outcome: 'succeeded' })],
    ['done', issue({ id: 'a', status: 'closed' })],
    ['failed', issue({ id: 'a', status: 'closed', outcome: 'failed' })],
    ['failed', issue({ id: 'a', status: 'closed', outcome: 'cancelled' })],
    ['running', issue({ id: 'a', status: 'in_progress' })],
    ['running', issue({ id: 'a', status: 'retry_wait' })],
    ['deferred', issue({ id: 'a', status: 'deferred' })],
    ['blocked', issue({ id: 'a', status: 'blocked' })],
    ['blocked', issue({ id: 'a', dispatchState: 'terminally_blocked' })],
    ['ready', issue({ id: 'a', dispatchState: 'ready' })],
    ['waiting', issue({ id: 'a', dispatchState: 'waiting' })],
  ])('maps %s', (want, input) => {
    expect(factoryIssueState(input)).toBe(want);
  });
});

describe('factoryGraphModel', () => {
  it('is empty when only containers exist', () => {
    expect(factoryGraphModel([issue({ id: 'e.1', kind: 'mol' })])).toEqual({ nodes: [], edges: [] });
    expect(factoryGraphModel([])).toEqual({ nodes: [], edges: [] });
  });

  it('lifts children of the invisible Mol to the top and keeps declared edges', () => {
    const { nodes, edges } = factoryGraphModel([
      issue({ id: 'e.1', kind: 'mol', title: 'Root Mol' }),
      issue({ id: 'e.1.1', parentId: 'e.1', kind: 'implementation', title: 'Backend', status: 'closed', outcome: 'succeeded' }),
      issue({ id: 'e.1.2', parentId: 'e.1', title: 'Frontend', dispatchState: 'waiting', dependsOn: [{ id: 'e.1.1', type: 'blocks' }] }),
      issue({ id: 'e.1.2.1', parentId: 'e.1.2', title: 'Subtask', dispatchState: 'ready' }),
    ]);
    expect(nodes.map((node) => node.id)).toEqual(['e.1.1', 'e.1.2', 'e.1.2.1']);
    expect(nodes.map((node) => node.state)).toEqual(['done', 'waiting', 'ready']);
    // The Mol parent produced no dangling hierarchy edge.
    expect(edges).toEqual([
      { id: 'blocks:e.1.1->e.1.2', source: 'e.1.1', target: 'e.1.2', kind: 'blocks' },
      { id: 'hierarchy:e.1.2->e.1.2.1', source: 'e.1.2', target: 'e.1.2.1', kind: 'hierarchy' },
    ]);
  });

  it('places dependents below what they wait for', () => {
    const { nodes } = factoryGraphModel([
      issue({ id: 'a' }),
      issue({ id: 'b', dependsOn: [{ id: 'a', type: 'blocks' }] }),
      issue({ id: 'c', dependsOn: [{ id: 'b', type: 'blocks' }, { id: 'a', type: 'blocks' }] }),
      issue({ id: 'sibling' }),
    ]);
    const y = Object.fromEntries(nodes.map((node) => [node.id, node.y]));
    expect(y.a).toBeLessThan(y.b);
    // The longest path wins: c sits below b even though it also depends on a.
    expect(y.b).toBeLessThan(y.c);
    // Same-layer nodes are spread across columns instead of stacking.
    expect(nodes.find((node) => node.id === 'sibling')!.y).toBe(y.a);
    expect(nodes.find((node) => node.id === 'sibling')!.x).toBeGreaterThan(nodes.find((node) => node.id === 'a')!.x);
  });

  it('still draws every node when dependencies form a cycle', () => {
    const { nodes, edges } = factoryGraphModel([
      issue({ id: 'a', dependsOn: [{ id: 'b', type: 'blocks' }] }),
      issue({ id: 'b', dependsOn: [{ id: 'a', type: 'blocks' }] }),
    ]);
    expect(nodes).toHaveLength(2);
    expect(edges).toHaveLength(2);
  });

  it('labels on_failure edges and drops unknown or self references', () => {
    const { edges } = factoryGraphModel([
      issue({ id: 'a', dependsOn: [{ id: 'gone', type: 'blocks' }, { id: 'a', type: 'blocks' }] }),
      issue({ id: 'b', dependsOn: [{ id: 'a', type: 'on_failure' }, { id: 'a', type: 'on_failure' }] }),
    ]);
    expect(edges).toEqual([{ id: 'on_failure:a->b', source: 'a', target: 'b', kind: 'on_failure' }]);
  });
});
