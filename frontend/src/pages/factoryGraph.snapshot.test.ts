import { describe, expect, it } from 'vitest';
import type { FactoryIssue } from '../lib/api';
import { factoryGraphModel, proposalIssues } from './factoryGraph';

const issue = (id: string, overrides: Partial<FactoryIssue> = {}): FactoryIssue => ({ id, epicId: 'ship', project: '/repo', kind: 'task', title: id, status: 'open', ...overrides });

describe('frozen graph approval preview', () => {
  const issues = [
    issue('old-parent', { kind: 'mol' }),
    issue('new-parent', { kind: 'mol' }),
    issue('phase', { kind: 'phase', parentId: 'new-parent' }),
    issue('work', { parentId: 'phase', dependsOn: [{ id: 'other.1', type: 'blocks' }] }),
  ];
  const externalIssues = [issue('other.1', { epicId: 'other', title: 'External blocker', status: 'closed', outcome: 'succeeded' })];

  it('shows frozen containers, reparenting and the external dependency endpoint', () => {
    const manifest = { epicId: 'ship', molId: 'old-parent', project: '/repo', nodes: [], issues, externalIssues };
    const preview = proposalIssues(manifest);
    expect(preview.map((item) => item.id)).toEqual(['old-parent', 'new-parent', 'phase', 'work', 'other.1']);
    expect(preview.at(-1)?.title).toBe('other: External blocker');
    const { nodes, edges } = factoryGraphModel(preview, true);
    expect(nodes.map((node) => node.id)).toEqual(preview.map((item) => item.id));
    expect(edges).toEqual(expect.arrayContaining([
      expect.objectContaining({ source: 'new-parent', target: 'phase', kind: 'hierarchy' }),
      expect.objectContaining({ source: 'phase', target: 'work', kind: 'hierarchy' }),
      expect.objectContaining({ source: 'other.1', target: 'work', kind: 'blocks' }),
    ]));
    expect(edges.some((edge) => edge.source === 'old-parent')).toBe(false);
    expect(externalIssues[0].title).toBe('External blocker');
  });

  it('keeps linked and unlinked revisions independent', () => {
    const linked = { epicId: 'ship', molId: 'old-parent', project: '/repo', nodes: [], issues, externalIssues };
    const unlinked = { ...linked, issues: issues.map((item) => item.id === 'work' ? { ...item, dependsOn: [] } : item), externalIssues: [] };
    const before = factoryGraphModel(proposalIssues(linked), true);
    const after = factoryGraphModel(proposalIssues(unlinked), true);
    expect(before.edges.some((edge) => edge.source === 'other.1')).toBe(true);
    expect(after.edges.some((edge) => edge.source === 'other.1')).toBe(false);
    expect(after.nodes.some((node) => node.id === 'other.1')).toBe(false);
  });
});
