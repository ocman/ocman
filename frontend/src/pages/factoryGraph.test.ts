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

  it('hangs a recovery or authority gate off the work it interrupted', () => {
    const gate = { issueId: 'e.1.3', epicId: 'epic-1', attemptId: 'a1', workId: 'e.1.1', question: 'Now what?', reason: 'stuck', choices: [], resolution: 'open' };
    const { edges } = factoryGraphModel([
      issue({ id: 'e.1', kind: 'mol' }),
      issue({ id: 'e.1.1', parentId: 'e.1', kind: 'implementation', title: 'Backend', status: 'in_progress' }),
      issue({ id: 'e.1.3', parentId: 'e.1', kind: 'gate', title: 'Recovery gate', recovery: gate }),
      issue({ id: 'e.1.4', parentId: 'e.1', kind: 'gate', title: 'Permission gate', authority: { ...gate, issueId: 'e.1.4', requestId: 'r1', permission: 'bash', target: 'rm', workId: 'e.1.1' } }),
    ]);
    // Both gates point at the interrupted work instead of floating loose under the Mol.
    expect(edges).toEqual([
      { id: 'interrupts:e.1.1->e.1.3', source: 'e.1.1', target: 'e.1.3', kind: 'interrupts' },
      { id: 'interrupts:e.1.1->e.1.4', source: 'e.1.1', target: 'e.1.4', kind: 'interrupts' },
    ]);
  });

  it('leaves no node stranded for a real epic with resolved gates', () => {
    // Shape taken from aamruifdam-crov: five recovery gates and one authority gate,
    // all resolved, hanging off two implementations that carry no edge to them.
    const gate = (id: string, workId: string) => issue({ id, parentId: 'e.1', kind: 'gate', title: 'Recovery gate', status: 'closed', outcome: 'succeeded', recovery: { issueId: id, epicId: 'epic-1', attemptId: 'a', workId, question: 'q', reason: 'r', choices: [], resolution: 'resume' } });
    const { nodes, edges } = factoryGraphModel([
      issue({ id: 'e.1', kind: 'mol' }),
      issue({ id: 'e.1.3', parentId: 'e.1', kind: 'plan', status: 'closed', outcome: 'succeeded' }),
      issue({ id: 'e.1.1', parentId: 'e.1', kind: 'gate', title: 'Approval gate', status: 'closed', outcome: 'succeeded', dependsOn: [{ id: 'e.1.3', type: 'blocks' }] }),
      issue({ id: 'e.1.2', parentId: 'e.1', kind: 'materialization', status: 'closed', outcome: 'succeeded', dependsOn: [{ id: 'e.1.1', type: 'blocks' }] }),
      issue({ id: 'e.1.4', parentId: 'e.1', kind: 'implementation', status: 'closed', outcome: 'succeeded', dependsOn: [{ id: 'e.1.2', type: 'blocks' }] }),
      issue({ id: 'e.1.5', parentId: 'e.1', kind: 'implementation', status: 'closed', outcome: 'succeeded', dependsOn: [{ id: 'e.1.2', type: 'blocks' }, { id: 'e.1.4', type: 'blocks' }] }),
      gate('e.1.10', 'e.1.4'),
      gate('e.1.11', 'e.1.4'),
      issue({ id: 'e.1.12', parentId: 'e.1', kind: 'gate', title: 'Authority escalation gate', status: 'closed', outcome: 'succeeded', authority: { issueId: 'e.1.12', epicId: 'epic-1', attemptId: 'a', requestId: 'r', workId: 'e.1.5', permission: 'bash', target: 'rm', resolution: 'approve' } }),
      gate('e.1.13', 'e.1.5'),
    ]);
    const connected = new Set(edges.flatMap((edge) => [edge.source, edge.target]));
    // Only the plan is a root; every gate hangs off something.
    expect(nodes.filter((node) => !connected.has(node.id)).map((node) => node.id)).toEqual([]);
    expect(edges.filter((edge) => edge.kind === 'interrupts').map((edge) => `${edge.source}->${edge.target}`)).toEqual([
      'e.1.4->e.1.10', 'e.1.4->e.1.11', 'e.1.5->e.1.12', 'e.1.5->e.1.13',
    ]);
  });

  it('labels on_failure edges and drops unknown or self references', () => {
    const { edges } = factoryGraphModel([
      issue({ id: 'a', dependsOn: [{ id: 'gone', type: 'blocks' }, { id: 'a', type: 'blocks' }] }),
      issue({ id: 'b', dependsOn: [{ id: 'a', type: 'on_failure' }, { id: 'a', type: 'on_failure' }] }),
    ]);
    expect(edges).toEqual([{ id: 'on_failure:a->b', source: 'a', target: 'b', kind: 'on_failure' }]);
  });
});
