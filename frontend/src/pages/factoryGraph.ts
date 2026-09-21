// Shared Dagre layout for epic, proposal, and Formula graphs.
import { graphlib, layout } from '@dagrejs/dagre';
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

export const GRAPH_NODE_WIDTH = 200;
export const GRAPH_NODE_HEIGHT = 100;

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

  const graph = new graphlib.Graph().setGraph({ rankdir: 'TB', nodesep: 60, ranksep: 60 }).setDefaultEdgeLabel(() => ({}));
  const owners = new Map(edges.filter((edge) => edge.kind === 'interrupts').map((edge) => [edge.target, edge.source]));
  const decisions = new Map<string, string[]>();
  // Stable input order keeps status updates and API sorting from moving the graph.
  const ordered = [...visible].sort((a, b) => a.id.localeCompare(b.id));
  for (const issue of ordered) {
    const owner = owners.get(issue.id);
    if (owner) decisions.set(owner, [...(decisions.get(owner) ?? []), issue.id]);
  }
  for (const issue of ordered) {
    if (owners.has(issue.id)) continue;
    const count = decisions.get(issue.id)?.length ?? 0;
    // Reserve a side column so decision history cannot stretch the execution ranks.
    graph.setNode(issue.id, { width: count ? GRAPH_NODE_WIDTH * 2 + 60 : GRAPH_NODE_WIDTH, height: Math.max(1, count) * (GRAPH_NODE_HEIGHT + 20) - 20 });
  }
  for (const edge of [...edges].sort((a, b) => a.id.localeCompare(b.id))) {
    const source = owners.get(edge.source) ?? edge.source;
    const target = owners.get(edge.target) ?? edge.target;
    if (source !== target) graph.setEdge(source, target);
  }
  layout(graph);
  const positions = new Map<string, { x: number; y: number }>();
  for (const id of graph.nodes()) {
    const { x, y, width, height } = graph.node(id);
    positions.set(id, { x: x - width / 2, y: y - GRAPH_NODE_HEIGHT / 2 });
    for (const [index, decision] of (decisions.get(id) ?? []).entries()) {
      positions.set(decision, { x: x - width / 2 + GRAPH_NODE_WIDTH + 60, y: y - height / 2 + index * (GRAPH_NODE_HEIGHT + 20) });
    }
  }
  const nodes = visible.map((issue) => {
    return { id: issue.id, issue, state: factoryIssueState(issue), ...positions.get(issue.id)! };
  });
  return { nodes, edges };
}
