// Layout for the epic work graph: hierarchy and declared dependencies placed on
// layers, with status as a class name so the palette stays in CSS.
import type { FactoryIssue } from '../lib/api';

export type GraphState = 'done' | 'failed' | 'running' | 'blocked' | 'ready' | 'waiting' | 'deferred';

export const GRAPH_STATES: GraphState[] = ['done', 'failed', 'running', 'blocked', 'ready', 'waiting', 'deferred'];

export interface GraphNode {
  id: string;
  issue: FactoryIssue;
  state: GraphState;
  x: number;
  y: number;
}

export interface GraphEdge {
  id: string;
  source: string;
  target: string;
  kind: 'hierarchy' | 'blocks' | 'on_failure';
}

const COLUMN = 260;
const ROW = 120;

export function factoryIssueState(issue: FactoryIssue): GraphState {
  if (issue.status === 'closed') return issue.outcome && issue.outcome !== 'succeeded' ? 'failed' : 'done';
  if (issue.status === 'in_progress' || issue.status === 'retry_wait') return 'running';
  if (issue.status === 'deferred') return 'deferred';
  if (issue.status === 'blocked' || issue.dispatchState === 'blocked' || issue.dispatchState === 'terminally_blocked') return 'blocked';
  if (issue.dispatchState === 'ready') return 'ready';
  return 'waiting';
}

export function factoryGraphModel(issues: FactoryIssue[]): { nodes: GraphNode[]; edges: GraphEdge[] } {
  // ponytail: Mols are invisible containers everywhere else in the UI; their
  // children are lifted to the nearest visible ancestor instead.
  const byID = new Map(issues.map((issue) => [issue.id, issue]));
  const visible = issues.filter((issue) => issue.kind !== 'mol');
  const shown = new Set(visible.map((issue) => issue.id));
  if (!visible.length) return { nodes: [], edges: [] };
  const visibleAncestor = (id?: string): string | undefined => {
    for (let current = id ? byID.get(id) : undefined; current; current = current.parentId ? byID.get(current.parentId) : undefined) {
      if (shown.has(current.id)) return current.id;
    }
    return undefined;
  };

  const edges: GraphEdge[] = [];
  const seen = new Set<string>();
  const add = (source: string | undefined, target: string, kind: GraphEdge['kind']) => {
    if (!source || source === target) return;
    const id = `${kind}:${source}->${target}`;
    if (seen.has(id)) return;
    seen.add(id);
    edges.push({ id, source, target, kind });
  };
  for (const issue of visible) {
    add(visibleAncestor(issue.parentId), issue.id, 'hierarchy');
    for (const edge of issue.dependsOn ?? []) add(visibleAncestor(edge.id), issue.id, edge.type === 'on_failure' ? 'on_failure' : 'blocks');
  }

  // Longest-path layering over a DAG. A cycle would starve Kahn's queue, so
  // whatever is left keeps its current depth and still gets drawn.
  const depth = new Map(visible.map((issue) => [issue.id, 0]));
  const incoming = new Map(visible.map((issue) => [issue.id, 0]));
  const outgoing = new Map<string, GraphEdge[]>();
  for (const edge of edges) {
    incoming.set(edge.target, (incoming.get(edge.target) ?? 0) + 1);
    outgoing.set(edge.source, [...(outgoing.get(edge.source) ?? []), edge]);
  }
  const queue = visible.filter((issue) => !incoming.get(issue.id)).map((issue) => issue.id);
  for (let head = 0; head < queue.length; head++) {
    const id = queue[head];
    for (const edge of outgoing.get(id) ?? []) {
      depth.set(edge.target, Math.max(depth.get(edge.target) ?? 0, (depth.get(id) ?? 0) + 1));
      incoming.set(edge.target, (incoming.get(edge.target) ?? 1) - 1);
      if (!incoming.get(edge.target)) queue.push(edge.target);
    }
  }

  const used = new Map<number, number>();
  const nodes = visible.map((issue) => {
    const row = depth.get(issue.id) ?? 0;
    const column = used.get(row) ?? 0;
    used.set(row, column + 1);
    return { id: issue.id, issue, state: factoryIssueState(issue), x: column * COLUMN, y: row * ROW };
  });
  return { nodes, edges };
}
