import { describe, expect, it } from 'vitest';
import { factoryGraphDiagram, factoryIssueState } from './factoryGraph';
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

describe('factoryGraphDiagram', () => {
  it('is empty when only containers exist', () => {
    expect(factoryGraphDiagram([issue({ id: 'e.1', kind: 'mol' })])).toBe('');
    expect(factoryGraphDiagram([])).toBe('');
  });

  it('lifts children of the invisible Mol to the top and keeps declared edges', () => {
    const source = factoryGraphDiagram([
      issue({ id: 'e.1', kind: 'mol', title: 'Root Mol' }),
      issue({ id: 'e.1.1', parentId: 'e.1', kind: 'implementation', title: 'Backend', status: 'closed', outcome: 'succeeded' }),
      issue({ id: 'e.1.2', parentId: 'e.1', title: 'Frontend', dispatchState: 'waiting', dependsOn: [{ id: 'e.1.1', type: 'blocks' }] }),
      issue({ id: 'e.1.2.1', parentId: 'e.1.2', title: 'Subtask', dispatchState: 'ready' }),
    ]);
    expect(source).not.toContain('Root Mol');
    expect(source).toContain('n0["Backend<br/>implementation · done"]:::done');
    expect(source).toContain('n1["Frontend<br/>task · waiting"]:::waiting');
    expect(source).toContain('  n0 --> n1');
    expect(source).toContain('  n1 -.-> n2');
    // The Mol parent produced no dangling hierarchy edge.
    expect(source).not.toContain('-.-> n0');
  });

  it('labels on_failure edges and neutralises quotes in titles', () => {
    const source = factoryGraphDiagram([
      issue({ id: 'a', title: 'Ship "it"\nnow' }),
      issue({ id: 'b', title: 'Recover', dependsOn: [{ id: 'a', type: 'on_failure' }] }),
    ]);
    expect(source).toContain('n0["Ship #quot;it#quot; now<br/>task · waiting"]');
    expect(source).toContain('  n0 -- on failure --> n1');
  });

  it('ignores edges to unknown or self nodes', () => {
    const source = factoryGraphDiagram([issue({ id: 'a', dependsOn: [{ id: 'gone', type: 'blocks' }, { id: 'a', type: 'blocks' }] })]);
    expect(source.split('\n').filter((line) => line.includes('-->'))).toEqual([]);
  });
});
