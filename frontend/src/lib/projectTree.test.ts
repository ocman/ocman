import { describe, it, expect } from 'vitest';
import { buildScopeTree, matchesScope, type ScopeNode } from './projectTree';

// Helper: walk the tree depth-first and return [path, projectCount, depth]
// triples, useful for asserting overall shape without writing huge nested
// object literals in every test.
function flatten(tree: ScopeNode[], depth = 0): Array<[string, number, number]> {
  const out: Array<[string, number, number]> = [];
  for (const node of tree) {
    out.push([node.path, node.projectCount, depth]);
    out.push(...flatten(node.children, depth + 1));
  }
  return out;
}

describe('buildScopeTree', () => {
  it('returns empty array for no projects', () => {
    expect(buildScopeTree([])).toEqual([]);
  });

  it('handles a single project', () => {
    const tree = buildScopeTree([{ directory: '/Users/dries/src/github.com/foo/bar' }]);
    // The single chain has no branching, so the collapse rule produces
    // one root node whose path IS the project, with no children.
    expect(tree).toHaveLength(1);
    expect(tree[0].path).toBe('/Users/dries/src/github.com/foo/bar');
    expect(tree[0].projectCount).toBe(1);
    expect(tree[0].children).toEqual([]);
  });

  it('exposes shared parent nodes when projects branch', () => {
    const tree = buildScopeTree([
      { directory: '/Users/dries/src/github.com/foo/bar' },
      { directory: '/Users/dries/src/github.com/foo/baz' },
    ]);
    // Branching point should be at /Users/dries/src/github.com/foo
    // (the deepest shared prefix).
    const flat = flatten(tree);
    // First row is the collapsed shared prefix.
    expect(flat[0][0]).toBe('/Users/dries/src/github.com/foo');
    expect(flat[0][1]).toBe(2);
    // Then two children, the leaves.
    const childPaths = flat.slice(1).map((r) => r[0]).sort();
    expect(childPaths).toEqual([
      '/Users/dries/src/github.com/foo/bar',
      '/Users/dries/src/github.com/foo/baz',
    ]);
  });

  it('exposes every level when branching happens at multiple depths (the user example)', () => {
    // Mirrors the conversation: github.com hosts two orgs, one of
    // which hosts two repos. Every branching level should be a
    // selectable node so users can scope to the host, the org, or
    // an individual repo.
    const tree = buildScopeTree([
      { directory: '/Users/dries/src/github.com/nousefreak/ocman' },
      { directory: '/Users/dries/src/github.com/nousefreak/other' },
      { directory: '/Users/dries/src/github.com/some-org/whatever' },
    ]);

    const flat = flatten(tree);
    const paths = flat.map((r) => r[0]);
    // Host level is shared by both orgs ⇒ branching ⇒ visible.
    expect(paths).toContain('/Users/dries/src/github.com');
    // Org level for nousefreak hosts two repos ⇒ branching ⇒ visible.
    expect(paths).toContain('/Users/dries/src/github.com/nousefreak');
    // Both repos under nousefreak.
    expect(paths).toContain('/Users/dries/src/github.com/nousefreak/ocman');
    expect(paths).toContain('/Users/dries/src/github.com/nousefreak/other');
    // The other org is a single-leaf chain — collapsed to its repo path.
    expect(paths).toContain('/Users/dries/src/github.com/some-org/whatever');
    // Counts: host contains all 3, nousefreak contains 2.
    const host = flat.find((r) => r[0] === '/Users/dries/src/github.com')!;
    const org = flat.find((r) => r[0] === '/Users/dries/src/github.com/nousefreak')!;
    expect(host[1]).toBe(3);
    expect(org[1]).toBe(2);
  });

  it('collapses single-child chains so users do not see useless intermediate rows', () => {
    // When every project is under /a/b/c and there is no other tree,
    // the tree should not render /a, /a/b separately — just /a/b/c.
    const tree = buildScopeTree([
      { directory: '/a/b/c/foo' },
      { directory: '/a/b/c/bar' },
    ]);
    const flat = flatten(tree);
    // One root at the branching point, two leaves.
    expect(flat).toHaveLength(3);
    expect(flat[0][0]).toBe('/a/b/c');
  });

  it('skips empty / blank directories', () => {
    const tree = buildScopeTree([
      { directory: '' },
      { directory: '   ' },
      { directory: '/real/project' },
    ]);
    const paths = flatten(tree).map((r) => r[0]);
    expect(paths).toEqual(['/real/project']);
  });

  it('deduplicates exact-duplicate directories without inflating projectCount', () => {
    const tree = buildScopeTree([
      { directory: '/repo/foo' },
      { directory: '/repo/foo' },
    ]);
    expect(tree).toHaveLength(1);
    expect(tree[0].path).toBe('/repo/foo');
    expect(tree[0].projectCount).toBe(1);
  });

  it('does NOT confuse a sibling whose path starts with another project', () => {
    // /repo/foo and /repo/foobar must end up as siblings, not parent/child,
    // even though one path is a string-prefix of the other. The fix is that
    // we split on '/' before insertion.
    const tree = buildScopeTree([
      { directory: '/repo/foo' },
      { directory: '/repo/foobar' },
    ]);
    // Branching point at /repo, two leaves.
    const flat = flatten(tree);
    expect(flat[0][0]).toBe('/repo');
    const childPaths = flat.slice(1).map((r) => r[0]).sort();
    expect(childPaths).toEqual(['/repo/foo', '/repo/foobar']);
  });

  it('uses the last segment as the display label', () => {
    const tree = buildScopeTree([
      { directory: '/Users/dries/src/github.com/foo/bar' },
      { directory: '/Users/dries/src/github.com/foo/baz' },
    ]);
    expect(tree[0].label).toBe('foo'); // last segment of the collapsed branching node
    expect(tree[0].children.map((c) => c.label).sort()).toEqual(['bar', 'baz']);
  });

  it('sorts siblings alphabetically by label for stable rendering', () => {
    const tree = buildScopeTree([
      { directory: '/r/zeta' },
      { directory: '/r/alpha' },
      { directory: '/r/middle' },
    ]);
    expect(tree[0].children.map((c) => c.label)).toEqual(['alpha', 'middle', 'zeta']);
  });
});

describe('matchesScope', () => {
  it('matches everything when scope is empty', () => {
    expect(matchesScope('/anywhere', '')).toBe(true);
    expect(matchesScope('', '')).toBe(true);
  });

  it('matches the directory itself', () => {
    expect(matchesScope('/repo/foo', '/repo/foo')).toBe(true);
  });

  it('matches descendants', () => {
    expect(matchesScope('/repo/foo/sub', '/repo/foo')).toBe(true);
    expect(matchesScope('/repo/foo/a/b', '/repo/foo')).toBe(true);
  });

  it('rejects siblings whose path starts with the scope as a string', () => {
    // The whole point of AD-7: '/repo/foobar' must NOT match scope '/repo/foo'.
    expect(matchesScope('/repo/foobar', '/repo/foo')).toBe(false);
  });

  it('rejects unrelated directories', () => {
    expect(matchesScope('/elsewhere', '/repo/foo')).toBe(false);
  });

  it('treats a trailing slash on the scope as the same scope', () => {
    expect(matchesScope('/repo/foo', '/repo/foo/')).toBe(true);
    expect(matchesScope('/repo/foo/sub', '/repo/foo/')).toBe(true);
    expect(matchesScope('/repo/foobar', '/repo/foo/')).toBe(false);
  });
});
