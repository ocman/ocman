import { describe, expect, it } from 'vitest';
import { GRAPH_NODE_HEIGHT, GRAPH_NODE_WIDTH, factoryGraphModel, factoryIssueState, formulaIssues, proposalIssues } from './factoryGraph';
import type { FactoryIssue } from '../lib/api';

const issue = (overrides: Partial<FactoryIssue> & Pick<FactoryIssue, 'id'>): FactoryIssue => ({
  epicId: 'epic-1', project: '/repo', kind: 'task', title: 'Work', status: 'open', ...overrides,
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
    ['waiting', issue({ id: 'a', kind: 'phase', dispatchState: 'ready' })],
    ['waiting', issue({ id: 'a', dispatchState: 'waiting' })],
  ])('maps %s', (want, input) => {
    expect(factoryIssueState(input)).toBe(want);
  });
});

describe('factoryGraphModel', () => {
  it('centers forks and joins and keeps the layout stable across status and input-order changes', () => {
    const issues = [
      issue({ id: 'start' }),
      issue({ id: 'left', dependsOn: [{ id: 'start', type: 'blocks' }] }),
      issue({ id: 'right', dependsOn: [{ id: 'start', type: 'blocks' }] }),
      issue({ id: 'join', dependsOn: [{ id: 'left', type: 'blocks' }, { id: 'right', type: 'blocks' }] }),
    ];
    const coordinates = (input: FactoryIssue[]) => Object.fromEntries(factoryGraphModel(input).nodes.map(({ id, x, y }) => [id, { x, y }]));
    const positions = coordinates(issues);
    expect(positions.start.x).toBe((positions.left.x + positions.right.x) / 2);
    expect(positions.join.x).toBe(positions.start.x);
    expect(Math.abs(positions.left.x - positions.right.x)).toBeGreaterThan(GRAPH_NODE_WIDTH);
    expect(coordinates(issues.toReversed().map((item) => ({ ...item, status: 'closed', outcome: 'succeeded', dependsOn: item.dependsOn?.toReversed() })))).toEqual(positions);
  });

  it('packs many decisions beside their task without overlaps or moving delivery ahead of work', () => {
    const { nodes } = factoryGraphModel([
      issue({ id: 'work', status: 'in_progress' }),
      ...Array.from({ length: 15 }, (_, i) => issue({ id: `gate-${i}`, kind: 'gate', authority: { issueId: `gate-${i}`, epicId: 'epic-1', attemptId: 'a', workId: 'work', requestId: 'r', permission: 'bash', target: 'test', resolution: 'approve' } })),
      issue({ id: 'deliver', kind: 'delivery', dependsOn: [{ id: 'work', type: 'blocks' }] }),
    ]);
    for (const a of nodes) for (const b of nodes) {
      if (a.id === b.id) continue;
      expect(Math.abs(a.x - b.x) >= GRAPH_NODE_WIDTH || Math.abs(a.y - b.y) >= GRAPH_NODE_HEIGHT).toBe(true);
    }
    const delivery = nodes.find((node) => node.id === 'deliver')!;
    expect(nodes.filter((node) => node !== delivery).every((node) => node.y + GRAPH_NODE_HEIGHT < delivery.y)).toBe(true);
    expect(Math.max(...nodes.map((node) => node.x)) - Math.min(...nodes.map((node) => node.x))).toBeLessThan(3 * GRAPH_NODE_WIDTH);
  });

  it('keeps an unmaterialized phase visible', () => {
    const { nodes, edges } = factoryGraphModel([
      issue({ id: 'phase', kind: 'phase' }),
      issue({ id: 'verify', dependsOn: [{ id: 'phase', type: 'blocks' }] }),
    ]);
    expect(nodes.map((node) => node.id)).toEqual(['phase', 'verify']);
    expect(edges).toContainEqual(expect.objectContaining({ source: 'phase', target: 'verify', kind: 'blocks' }));
  });

  it.each(['blocks', 'on_failure', 'merge_gated'])('preserves %s edges through nested replaced phases', (type) => {
    const { nodes, edges } = factoryGraphModel([
      issue({ id: 'outer', kind: 'phase' }),
      issue({ id: 'inner', kind: 'phase', parentId: 'outer' }),
      issue({ id: 'task', parentId: 'inner' }),
      issue({ id: 'next', dependsOn: [{ id: 'outer', type }] }),
    ]);
    expect(nodes.map((node) => node.id)).toEqual(['task', 'next']);
    expect(edges).toEqual([{ id: `${type}:task->next`, source: 'task', target: 'next', kind: type }]);
  });

  it.each([
    [{ requirement: 'required' }, true],
    [{ requirement: 'reference' }, false],
    [{ requirement: 'required', dispatchState: 'not_applicable' }, false],
    [{ requirement: 'optional' }, true],
    [{ requirement: 'optional', status: 'closed' }, false],
    [{ requirement: 'optional', status: 'deferred' }, false],
    [{ requirement: 'optional', dispatchState: 'terminally_blocked' }, false],
  ] satisfies [Partial<FactoryIssue>, boolean][])('matches phase participation for %j', (overrides, participates) => {
    const { edges } = factoryGraphModel([
      issue({ id: 'phase', kind: 'phase' }),
      issue({ id: 'child', parentId: 'phase', ...overrides }),
      issue({ id: 'verify', dependsOn: [{ id: 'phase', type: 'blocks' }] }),
    ]);
    expect(edges.some((edge) => edge.source === 'child' && edge.target === 'verify' && edge.kind === 'blocks')).toBe(participates);
  });

  it('replaces the implementation phase with its tickets and keeps delivery after unfinished dependencies', () => {
    const { nodes, edges } = factoryGraphModel([
      issue({ id: 'approve', kind: 'gate', status: 'closed', outcome: 'succeeded' }),
      issue({ id: 'phase', kind: 'phase', dependsOn: [{ id: 'approve', type: 'blocks' }] }),
      issue({ id: 'first', kind: 'implementation', parentId: 'phase', requirement: 'required', status: 'closed', outcome: 'succeeded', dependsOn: [{ id: 'approve', type: 'blocks' }] }),
      issue({ id: 'last', kind: 'implementation', parentId: 'phase', requirement: 'required', status: 'in_progress', dependsOn: [{ id: 'first', type: 'blocks' }] }),
      issue({ id: 'permission', kind: 'gate', parentId: 'phase', requirement: 'reference', status: 'closed', outcome: 'succeeded', dependsOn: [{ id: 'last', type: 'blocks' }], authority: { issueId: 'permission', epicId: 'epic-1', attemptId: 'a', workId: 'last', requestId: 'r', permission: 'bash', target: 'test', resolution: 'approve' } }),
      issue({ id: 'verify', dependsOn: [{ id: 'phase', type: 'blocks' }] }),
      issue({ id: 'deliver', kind: 'delivery', dependsOn: [{ id: 'verify', type: 'blocks' }] }),
    ].reverse());
    const y = Object.fromEntries(nodes.map((node) => [node.id, node.y]));
    expect(nodes.map((node) => node.id)).not.toContain('phase');
    expect(y.last).toBeLessThan(y.verify);
    expect(y.verify).toBeLessThan(y.deliver);
    expect(y.permission).toBe(y.last);
    expect(nodes.find((node) => node.id === 'permission')!.x).toBeGreaterThan(nodes.find((node) => node.id === 'last')!.x);
    expect(edges).toContainEqual(expect.objectContaining({ source: 'last', target: 'verify', kind: 'blocks' }));
    expect(edges.every((edge) => edge.source !== 'phase' && edge.target !== 'phase')).toBe(true);
    expect(edges).not.toContainEqual(expect.objectContaining({ source: 'permission', target: 'verify' }));
  });

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

  it('draws one edge when a gate both stores and implies its link to the work', () => {
    const { edges } = factoryGraphModel([
      issue({ id: 'e.1', kind: 'mol' }),
      issue({ id: 'e.1.1', parentId: 'e.1', kind: 'implementation', title: 'Backend' }),
      issue({ id: 'e.1.2', parentId: 'e.1', kind: 'gate', title: 'Recovery gate', dependsOn: [{ id: 'e.1.1', type: 'blocks' }], recovery: { issueId: 'e.1.2', epicId: 'epic-1', attemptId: 'a', workId: 'e.1.1', question: 'q', reason: 'r', choices: [], resolution: 'open' } }),
    ]);
    expect(edges).toEqual([{ id: 'interrupts:e.1.1->e.1.2', source: 'e.1.1', target: 'e.1.2', kind: 'interrupts' }]);
  });

  it('labels on_failure edges and drops unknown or self references', () => {
    const { edges } = factoryGraphModel([
      issue({ id: 'a', dependsOn: [{ id: 'gone', type: 'blocks' }, { id: 'a', type: 'blocks' }] }),
      issue({ id: 'b', dependsOn: [{ id: 'a', type: 'on_failure' }, { id: 'a', type: 'on_failure' }] }),
    ]);
    expect(edges).toEqual([{ id: 'on_failure:a->b', source: 'a', target: 'b', kind: 'on_failure' }]);
  });

  it('keeps merge-gated delivery edges distinct', () => {
    const { edges } = factoryGraphModel([
      issue({ id: 'delivery', kind: 'delivery', status: 'closed', outcome: 'succeeded' }),
      issue({ id: 'app', dependsOn: [{ id: 'delivery', type: 'merge_gated' }] }),
    ]);
    expect(edges).toContainEqual({ id: 'merge_gated:delivery->app', source: 'delivery', target: 'app', kind: 'merge_gated' });
  });
});

describe('formulaIssues', () => {
  it('draws dependencies from prerequisite to dependent and preserves edge types', () => {
    const model = factoryGraphModel(formulaIssues({
      nodes: [{ key: 'plan', kind: 'plan' }, { key: 'approval', kind: 'gate' }, { key: 'recovery', kind: 'task' }],
      edges: [{ from: 'approval', to: 'plan' }, { from: 'recovery', to: 'approval', type: 'on_failure' }],
    }));
    expect(model.edges).toEqual([
      expect.objectContaining({ source: 'plan', target: 'approval', kind: 'blocks' }),
      expect.objectContaining({ source: 'approval', target: 'recovery', kind: 'on_failure' }),
    ]);
    expect(model.nodes.map((node) => node.issue.title)).toEqual(['plan', 'approval', 'recovery']);
    expect(model.nodes[0].y).toBeLessThan(model.nodes[1].y);
  });

  it('handles empty graphs and nullable API arrays', () => {
    expect(formulaIssues({ nodes: [], edges: [] })).toEqual([]);
    expect(formulaIssues({ nodes: null, edges: null } as never)).toEqual([]);
    expect(formulaIssues({ nodes: [{ key: 'plan', kind: 'plan' }], edges: null } as never)[0].dependsOn).toEqual([]);
  });
});

describe('proposalIssues', () => {
  it('turns manifest nodes and edges into drawable issues with blockers first', () => {
    const issues = proposalIssues({
      epicId: 'epic-1', molId: 'epic-1.1', project: '/repo',
      nodes: [
        { key: 'api', type: 'implementation', requirement: 'required', title: 'API' },
        { key: 'ui', type: 'implementation', requirement: 'required', title: 'UI', dependsOn: ['api'] },
        { key: 'docs', type: 'implementation', requirement: 'optional' },
      ],
      // Manifest edges point from the dependent to its blocker.
      edges: [{ from: 'docs', to: 'ui', type: 'on_failure' }],
    });
    expect(issues.map((issue) => [issue.id, issue.title, issue.dispatchState, issue.dependsOn])).toEqual([
      ['api', 'API', 'ready', undefined],
      ['ui', 'UI', 'waiting', [{ id: 'api', type: 'blocks' }]],
      ['docs', 'docs', 'waiting', [{ id: 'ui', type: 'on_failure' }]],
    ]);
    const { nodes, edges } = factoryGraphModel(issues);
    expect(nodes.map((node) => node.id)).toEqual(['api', 'ui', 'docs']);
    expect(nodes[0].y + GRAPH_NODE_HEIGHT).toBeLessThan(nodes[1].y);
    expect(nodes[1].y + GRAPH_NODE_HEIGHT).toBeLessThan(nodes[2].y);
    expect(edges.map((edge) => edge.id)).toEqual(['blocks:api->ui', 'on_failure:ui->docs']);
  });
});
