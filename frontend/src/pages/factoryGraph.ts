// Layout for the epic work graph: hierarchy and declared dependencies placed on
// layers, with status as a class name so the palette stays in CSS.
import type { FactoryFormula, FactoryIssue, FactoryProposal } from '../lib/api';

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
  kind: 'hierarchy' | 'completion' | 'blocks' | 'on_failure' | 'merge_gated' | 'interrupts';
}

const COLUMN = 260;
const ROW = 120;

export function factoryIssueState(issue: FactoryIssue): GraphState {
  if (issue.status === 'closed') return issue.outcome && issue.outcome !== 'succeeded' ? 'failed' : 'done';
  if (issue.status === 'in_progress' || issue.status === 'retry_wait') return 'running';
  if (issue.status === 'deferred') return 'deferred';
  if (issue.status === 'blocked' || issue.dispatchState === 'blocked' || issue.dispatchState === 'terminally_blocked') return 'blocked';
  if (issue.kind === 'phase') return 'waiting';
  if (issue.dispatchState === 'ready') return 'ready';
  return 'waiting';
}

// Shapes a not-yet-materialized proposal as issues so the epic graph can draw
// it. Nodes without blockers are 'ready', the rest 'waiting'.
export function proposalIssues(manifest: FactoryProposal['manifest']): FactoryIssue[] {
  const dependsOn = new Map<string, { id: string; type: string }[]>();
  const add = (key: string, id: string, type: string) => dependsOn.set(key, [...(dependsOn.get(key) ?? []), { id, type }]);
  for (const node of manifest.nodes) for (const dependency of node.dependsOn ?? []) add(node.key, dependency, 'blocks');
  // Manifest edges point from the dependent node to its blocker (see native.go).
  for (const edge of manifest.edges ?? []) add(edge.from, edge.to, edge.type);
  return manifest.nodes.map((node) => ({
    id: node.key,
    epicId: manifest.epicId,
    kind: node.type,
    requirement: node.requirement,
    title: node.title || node.key,
    description: node.description,
		project: node.project || manifest.project,
    status: 'open',
    dispatchState: dependsOn.has(node.key) ? 'waiting' : 'ready',
    dependsOn: dependsOn.get(node.key),
    manifestKey: node.key,
  }));
}

export function formulaIssues(formula: Pick<FactoryFormula, 'nodes' | 'edges' | 'steps'>): FactoryIssue[] {
  return (formula.nodes ?? []).map((node) => ({
    id: node.key, epicId: '', project: '', kind: node.kind, title: formula.steps?.[node.key]?.name || node.key, status: 'open',
    dependsOn: (formula.edges ?? []).filter((edge) => edge.from === node.key).map((edge) => ({ id: edge.to, type: edge.type ?? 'blocks' })),
  }));
}

export function factoryGraphModel(issues: FactoryIssue[]): { nodes: GraphNode[]; edges: GraphEdge[] } {
  // ponytail: Mols are invisible containers everywhere else in the UI; their
  // children are lifted to the nearest visible ancestor instead.
  const byID = new Map(issues.map((issue) => [issue.id, issue]));
  let visible = issues.filter((issue) => issue.kind !== 'mol');
  const shown = new Set(visible.map((issue) => issue.id));
  if (!visible.length) return { nodes: [], edges: [] };
  const visibleAncestor = (id?: string): string | undefined => {
    for (let current = id ? byID.get(id) : undefined; current; current = current.parentId ? byID.get(current.parentId) : undefined) {
      if (shown.has(current.id)) return current.id;
    }
    return undefined;
  };

  let edges: GraphEdge[] = [];
  // One edge per pair: a gate's link to the work it interrupted is both stored as
  // a dependency and derivable from its attempt, and drawing it twice is noise.
  const seen = new Set<string>();
  const add = (source: string | undefined, target: string, kind: GraphEdge['kind']) => {
    if (!source || source === target || seen.has(`${source}->${target}`)) return;
    seen.add(`${source}->${target}`);
    edges.push({ id: `${kind}:${source}->${target}`, source, target, kind });
  };
  for (const issue of visible) {
    // A recovery or authority gate is parented to its container, not to the work
    // it interrupted, which would leave it floating. Its attempt knows better.
    const interrupted = issue.recovery?.workId ?? issue.authority?.workId;
    const parent = visibleAncestor(issue.parentId);
    if (interrupted) add(visibleAncestor(interrupted), issue.id, 'interrupts');
    else if (parent && byID.get(parent)?.kind === 'phase') {
      // Match the workflow's phase-completion barrier, not parent-first execution.
      const excluded = issue.requirement === 'reference' || issue.dispatchState === 'not_applicable'
        || (issue.requirement === 'optional' && (issue.status === 'closed' || issue.status === 'deferred' || issue.dispatchState === 'terminally_blocked'));
      if (!excluded) add(issue.id, parent, 'completion');
    } else add(parent, issue.id, 'hierarchy');
    for (const edge of issue.dependsOn ?? []) {
      // The interrupted-work edge records provenance, not a completion prerequisite.
      if (edge.id === interrupted) continue;
      add(visibleAncestor(edge.id), issue.id, edge.type === 'on_failure' ? 'on_failure' : edge.type === 'merge_gated' ? 'merge_gated' : 'blocks');
    }
  }

  // Materialized tickets replace their phase placeholder. Route completion
  // dependencies through those tickets while leaving empty Formula steps visible.
  const replaced = visible.filter((phase) => phase.kind === 'phase' && issues.some((child) => child.parentId === phase.id && ['implementation', 'task', 'phase'].includes(child.kind)));
  for (const phase of replaced) {
    const incoming = edges.filter((edge) => edge.target === phase.id && edge.kind === 'completion');
    const outgoing = edges.filter((edge) => edge.source === phase.id);
    edges = edges.filter((edge) => edge.source !== phase.id && edge.target !== phase.id);
    for (const before of incoming) for (const after of outgoing) add(before.source, after.target, after.kind);
  }
  const replacedIDs = new Set(replaced.map((phase) => phase.id));
  visible = visible.filter((issue) => !replacedIDs.has(issue.id));

  // Longest-path layering over a DAG. A cycle would starve Kahn's queue, so
  // whatever is left keeps its current depth and still gets drawn.
  const depth = new Map(visible.map((issue) => [issue.id, 0]));
  const incoming = new Map(visible.map((issue) => [issue.id, 0]));
  const outgoing = new Map<string, GraphEdge[]>();
  for (const edge of edges) {
    if (edge.kind === 'interrupts') continue;
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

  // Decisions sit beside the work they interrupted; resolving one does not finish it.
  for (const edge of edges) {
    if (edge.kind === 'interrupts') depth.set(edge.target, depth.get(edge.source) ?? 0);
  }

  const used = new Map<number, number>();
  // Keep the execution path on the left even when old decisions arrive first.
  const nodes = [...visible].sort((a, b) => Number(Boolean(a.recovery || a.authority)) - Number(Boolean(b.recovery || b.authority))).map((issue) => {
    const row = depth.get(issue.id) ?? 0;
    const column = used.get(row) ?? 0;
    used.set(row, column + 1);
    return { id: issue.id, issue, state: factoryIssueState(issue), x: column * COLUMN, y: row * ROW };
  });
  return { nodes, edges };
}
