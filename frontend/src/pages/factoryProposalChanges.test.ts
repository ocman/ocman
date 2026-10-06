import { expect, it } from 'vitest';
import type { FactoryIssue, FactoryProposal } from '../lib/api';
import { proposalBaseline, proposalChanges } from './factoryProposalChanges';
import { factoryGraphModel, proposalIssues } from './factoryGraph';

const issue = (id: string, overrides: Partial<FactoryIssue> = {}): FactoryIssue => ({ id, epicId: 'epic', project: '/repo', kind: 'task', title: id, status: 'open', ...overrides });
const manifest = (issues: FactoryIssue[]): FactoryProposal['manifest'] => ({ epicId: 'epic', molId: 'mol', project: '/repo', nodes: [], issues });

it.each(['snapshot', 'initial plan'])('counts a link-only amendment against %s as a connection, not new work', (format) => {
  const old: FactoryProposal['manifest'] = format === 'snapshot' ? manifest([issue('work')]) : { epicId: 'epic', molId: 'mol', project: '/repo', nodes: [{ key: 'work', type: 'implementation', requirement: 'required' }] };
  const next = { ...manifest([issue('work', { manifestKey: 'work', dependsOn: [{ id: 'external', type: 'blocks' }] })]), externalIssues: [issue('external', { epicId: 'other', requirement: 'reference' })] };
  expect(proposalChanges(next, old).addedIssues.size).toBe(0);
  expect(proposalChanges(next, old).addedEdges).toEqual(new Set(['blocks:external->work']));
  expect(proposalIssues(next).some((item) => item.id === 'external')).toBe(true);
});

it('shows a second dependency type and dependencies overlapping hierarchy', () => {
  const old = manifest([issue('parent'), issue('work', { dependsOn: [{ id: 'parent', type: 'blocks' }] })]);
  const next = manifest([issue('parent'), issue('work', { parentId: 'parent', dependsOn: [{ id: 'parent', type: 'blocks' }, { id: 'parent', type: 'on_failure' }] })]);
  expect(factoryGraphModel(proposalIssues(next), true).edges.map((edge) => edge.kind)).toEqual(['hierarchy', 'blocks', 'on_failure']);
  expect(proposalChanges(next, old).addedEdges).toEqual(new Set(['hierarchy:parent->work', 'on_failure:parent->work']));
});

it('compares to the approved baseline even when newer unapproved revisions exist', () => {
  const baseline: FactoryProposal = { revision: 2, contentHash: 'base', manifest: manifest([issue('old')]) };
  const intermediate: FactoryProposal = { revision: 3, contentHash: 'middle', manifest: manifest([issue('old'), issue('first')]) };
  const current: FactoryProposal = { revision: 4, contentHash: 'new', manifest: { ...manifest([issue('old'), issue('first'), issue('second')]), baseRevision: 2 } };
  expect(proposalBaseline(current, [intermediate, current, baseline])).toBe(baseline);
  expect(proposalChanges(current.manifest, baseline.manifest).addedIssues).toEqual(new Set(['first', 'second']));
  expect(proposalBaseline({ ...current, manifest: { ...current.manifest, baseRevision: 0 } }, [baseline, intermediate])).toBeUndefined();
  expect(proposalBaseline({ ...current, manifest: manifest([]) }, [baseline, intermediate])).toBe(intermediate);
});

it('marks new hierarchy and external edges, leaving unchanged edges alone', () => {
  const old = manifest([issue('parent', { kind: 'mol' }), issue('work'), issue('blocker')]);
  const next = { ...manifest([issue('parent', { kind: 'mol' }), issue('work', { parentId: 'parent', dependsOn: [{ id: 'blocker', type: 'blocks' }, { id: 'external', type: 'merge_gated' }] }), issue('blocker')]), externalIssues: [issue('external', { epicId: 'other', kind: 'delivery' })] };
  expect(proposalChanges(next, old).addedEdges).toEqual(new Set(['hierarchy:parent->work', 'blocks:blocker->work', 'merge_gated:external->work']));
  expect(proposalChanges(next, next).addedEdges.size).toBe(0);
  expect(proposalChanges(next, next).addedIssues.size).toBe(0);
});

it('does not label existing Formula scaffolding or materialized work as additions', () => {
  const old: FactoryProposal['manifest'] = { epicId: 'epic', molId: 'mol', project: '/repo', nodes: [{ key: 'work', type: 'implementation', requirement: 'required' }] };
  const next = manifest([issue('mol', { kind: 'mol' }), issue('approve', { kind: 'gate' }), issue('runtime', { manifestKey: 'work', dependsOn: [{ id: 'approve', type: 'blocks' }] }), issue('verify', { workflow: { key: 'verify', kind: 'verification', config: {} } })]);
  expect(proposalChanges(next, old).addedIssues.size).toBe(0);
  expect(proposalChanges(next, old).addedEdges.size).toBe(0);
  expect(proposalChanges(old).addedIssues).toEqual(new Set(['work']));
});
