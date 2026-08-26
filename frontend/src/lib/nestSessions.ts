/**
 * Parent/child nesting for session lists.
 *
 * A session may descend from another via `parentId` (an OpenCode
 * native Task subagent). The
 * lists render this as a tree: each parent is immediately followed by
 * its children, indented by `depth`.
 *
 * The input is assumed to already be in the caller's preferred order
 * (typically recency-descending). That order is preserved both for the
 * top-level rows and, within each parent, for its children.
 */

type SessionLike = {
  id: string;
  parentId?: string;
};

export interface NestedRow<T> {
  session: T;
  /** 0 for top-level rows, 1 for direct children, etc. */
  depth: number;
  /** True when this row has at least one visible child below it. */
  hasChildren: boolean;
}

/**
 * Flattens `sessions` into a depth-ordered list where every child
 * directly follows its parent.
 *
 * Rules:
 *   - A session whose `parentId` is empty/undefined, or points at a
 *     session that is not present in the input, is treated as
 *     top-level (an "orphan" child is promoted rather than dropped).
 *   - Children of a parent keep their relative order from the input.
 *   - Cycles are broken: any session reachable only through a cycle is
 *     emitted once at the point it is first encountered.
 */
export function nestSessions<T extends SessionLike>(
  sessions: T[] | null | undefined,
): NestedRow<T>[] {
  if (!sessions || !sessions.length) return [];

  const byId = new Map<string, T>();
  for (const s of sessions) byId.set(s.id, s);

  // Bucket children under their (present) parent, preserving order.
  const childrenOf = new Map<string, T[]>();
  const roots: T[] = [];
  for (const s of sessions) {
    const parentId = s.parentId;
    if (parentId && parentId !== s.id && byId.has(parentId)) {
      const bucket = childrenOf.get(parentId);
      if (bucket) bucket.push(s);
      else childrenOf.set(parentId, [s]);
    } else {
      // Top-level: no parent, self-parent, or orphan child.
      roots.push(s);
    }
  }

  const out: NestedRow<T>[] = [];
  const visited = new Set<string>();

  const walk = (session: T, depth: number) => {
    if (visited.has(session.id)) return;
    visited.add(session.id);
    const kids = childrenOf.get(session.id) ?? [];
    out.push({ session, depth, hasChildren: kids.length > 0 });
    for (const kid of kids) walk(kid, depth + 1);
  };

  for (const root of roots) walk(root, 0);

  // Safety net: emit any session never reached (only possible inside a
  // pure cycle where no member is a root) as a top-level row so nothing
  // silently disappears from the list.
  for (const s of sessions) {
    if (!visited.has(s.id)) walk(s, 0);
  }

  return out;
}
