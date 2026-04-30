// projectTree.ts
//
// Pure helpers used by the Stats / Usage / Projects directory-prefix
// filter (see spec/stats-project-filter/architecture.md, AD-1 + AD-8).
//
// Two responsibilities:
//
//   - buildScopeTree:   turn a flat list of project directories into a
//                       collapsible tree of selectable prefixes. Branching
//                       happens whenever two projects diverge; non-branching
//                       chains are collapsed so the picker doesn't show
//                       useless intermediate rows.
//   - matchesScope:     mirror the SQL prefix predicate (AD-7) on the
//                       client. Used by the Projects tab, which filters its
//                       payload locally rather than re-fetching.
//
// Both functions are pure and exhaustively unit-tested in projectTree.test.ts.

export interface ScopeNode {
  /** Absolute path of this scope, e.g. '/Users/x/src/github.com'. */
  path: string;
  /** Last path segment, used as the display label. */
  label: string;
  /** Sub-scopes, sorted alphabetically by label. */
  children: ScopeNode[];
  /** Total number of leaf projects under this node (inclusive). */
  projectCount: number;
}

interface ProjectInput {
  directory: string;
}

// trieNode is the internal representation we build and then collapse
// into the public ScopeNode shape. Keeping it separate keeps the public
// type read-only and avoids the awkward "is this a leaf?" sentinel.
interface trieNode {
  segment: string; // last path component
  isLeaf: boolean; // true iff some input directory ends exactly here
  children: Map<string, trieNode>;
}

// segmentsOf splits an absolute path into its non-empty components.
// Leading '/' becomes an empty first element which we drop; the path
// reconstructs as '/' + segments.join('/').
function segmentsOf(directory: string): string[] {
  return directory.split('/').filter((s) => s.length > 0);
}

// pathFromSegments rebuilds an absolute path from segment components.
// We always treat the input as absolute (matches OpenCode's session.directory
// values, which are always absolute).
function pathFromSegments(segments: string[]): string {
  return '/' + segments.join('/');
}

/**
 * buildScopeTree converts a flat list of project directories into a
 * collapsible tree. The output rules:
 *
 *   - Empty / blank directories are skipped.
 *   - Exact duplicates count as one project.
 *   - Single-child chains are collapsed: if the only way through node N is
 *     into its single child C, and N itself is not a leaf, we drop N from
 *     the visible tree and bubble C up.
 *   - Each node's `path` is the absolute prefix it represents.
 *   - `projectCount` at any node is the count of distinct leaves in its
 *     subtree (so the root's projectCount equals the deduped input size).
 *   - Children are sorted alphabetically by label for stable rendering.
 */
export function buildScopeTree(projects: ProjectInput[]): ScopeNode[] {
  // 1. Deduplicate + drop blank directories.
  const seen = new Set<string>();
  const cleaned: string[] = [];
  for (const p of projects) {
    const dir = (p.directory ?? '').trim();
    if (!dir) continue;
    if (seen.has(dir)) continue;
    seen.add(dir);
    cleaned.push(dir);
  }
  if (cleaned.length === 0) return [];

  // 2. Build a trie keyed on path segments. The root is virtual — it
  //    holds the top-level segment children.
  const root: trieNode = { segment: '', isLeaf: false, children: new Map() };
  for (const dir of cleaned) {
    const segs = segmentsOf(dir);
    let node = root;
    for (const seg of segs) {
      let next = node.children.get(seg);
      if (!next) {
        next = { segment: seg, isLeaf: false, children: new Map() };
        node.children.set(seg, next);
      }
      node = next;
    }
    node.isLeaf = true;
  }

  // 3. Walk the trie, collapsing single-child chains and computing
  //    projectCount on the way back up.
  function walk(node: trieNode, ancestorSegments: string[]): ScopeNode[] {
    const out: ScopeNode[] = [];
    for (const child of node.children.values()) {
      const childSegments = [...ancestorSegments, child.segment];
      // Collapse: if the child is non-leaf and has exactly one
      // grandchild, fold the grandchild upward. Repeat until we
      // reach a leaf, a branching point, or a leaf-bearing internal
      // node (one that itself counts as a project).
      let cursor = child;
      let cursorSegments = childSegments;
      while (
        !cursor.isLeaf &&
        cursor.children.size === 1
      ) {
        const onlyChild = cursor.children.values().next().value as trieNode;
        cursor = onlyChild;
        cursorSegments = [...cursorSegments, onlyChild.segment];
      }
      const grandChildren = walk(cursor, cursorSegments);
      const ownLeaf = cursor.isLeaf ? 1 : 0;
      const projectCount =
        ownLeaf + grandChildren.reduce((sum, c) => sum + c.projectCount, 0);
      out.push({
        path: pathFromSegments(cursorSegments),
        label: cursor.segment,
        children: grandChildren,
        projectCount,
      });
    }
    out.sort((a, b) => a.label.localeCompare(b.label));
    return out;
  }

  return walk(root, []);
}

/**
 * FlatScopeOption is one row in a depth-first flattening of a ScopeNode
 * tree, used by ProjectScopePicker to render a flat <select> with
 * indented entries. Lives here so the projectTree module owns the full
 * tree → list conversion and ProjectScopePicker can stay component-only
 * (which keeps Vite's fast-refresh happy: components-only exports).
 */
export interface FlatScopeOption {
  path: string;
  label: string;
  depth: number;
  projectCount: number;
}

/**
 * flattenForOptions walks a ScopeNode tree depth-first and returns one
 * FlatScopeOption per node, in display order.
 */
export function flattenForOptions(tree: ScopeNode[]): FlatScopeOption[] {
  const out: FlatScopeOption[] = [];
  function walk(nodes: ScopeNode[], depth: number): void {
    for (const n of nodes) {
      out.push({ path: n.path, label: n.label, depth, projectCount: n.projectCount });
      if (n.children.length > 0) walk(n.children, depth + 1);
    }
  }
  walk(tree, 0);
  return out;
}

/**
 * matchesScope mirrors the SQL predicate from AD-7 on the client:
 *
 *   directory == scope || directory startsWith (scope + '/')
 *
 * Empty scope means "no filter" and matches everything.  Trailing slashes
 * on the scope are tolerated.  Critical edge case the implementation
 * guards against: '/repo/foobar' must NOT match scope '/repo/foo'.
 */
export function matchesScope(directory: string, scope: string): boolean {
  if (!scope) return true;
  // Normalise: strip a single trailing slash so '/repo/foo' and
  // '/repo/foo/' behave identically.
  const normalised = scope.endsWith('/') ? scope.slice(0, -1) : scope;
  if (directory === normalised) return true;
  return directory.startsWith(normalised + '/');
}
