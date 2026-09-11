// Mermaid source for the epic work graph: hierarchy as dotted edges,
// declared dependencies as solid ones, status as colour plus a text label so
// the diagram stays readable without colour vision.
import type { FactoryIssue } from '../lib/api';

type GraphState = 'done' | 'failed' | 'running' | 'blocked' | 'ready' | 'waiting' | 'deferred';

const STATE_LABEL: Record<GraphState, string> = {
  done: 'done',
  failed: 'failed',
  running: 'running',
  blocked: 'blocked',
  ready: 'ready',
  waiting: 'waiting',
  deferred: 'deferred',
};

// Catppuccin-ish fills matching the app palette; text stays light on all of them.
const STATE_STYLE: Record<GraphState, string> = {
  done: 'fill:#2a3b2f,stroke:#a6e3a1',
  failed: 'fill:#3f2a30,stroke:#f38ba8',
  running: 'fill:#293548,stroke:#89b4fa',
  blocked: 'fill:#3f3529,stroke:#fab387',
  ready: 'fill:#2c3446,stroke:#94e2d5',
  waiting: 'fill:#2a2b3c,stroke:#7f849c',
  deferred: 'fill:#2a2b3c,stroke:#585b70',
};

export function factoryIssueState(issue: FactoryIssue): GraphState {
  if (issue.status === 'closed') return issue.outcome && issue.outcome !== 'succeeded' ? 'failed' : 'done';
  if (issue.status === 'in_progress' || issue.status === 'retry_wait') return 'running';
  if (issue.status === 'deferred') return 'deferred';
  if (issue.status === 'blocked' || issue.dispatchState === 'blocked' || issue.dispatchState === 'terminally_blocked') return 'blocked';
  if (issue.dispatchState === 'ready') return 'ready';
  return 'waiting';
}

// Mermaid labels are quoted strings; only the quote and newlines can break out.
function label(text: string) {
  return text.replaceAll('"', '#quot;').replace(/\s+/g, ' ').trim();
}

export function factoryGraphDiagram(issues: FactoryIssue[]): string {
  // ponytail: Mols are invisible containers everywhere else in the UI; their
  // children are lifted to the nearest visible ancestor instead.
  const byID = new Map(issues.map((issue) => [issue.id, issue]));
  const visible = issues.filter((issue) => issue.kind !== 'mol');
  if (!visible.length) return '';
  const nodeID = new Map(visible.map((issue, index) => [issue.id, `n${index}`]));
  const visibleAncestor = (id?: string): string | undefined => {
    for (let current = id ? byID.get(id) : undefined; current; current = current.parentId ? byID.get(current.parentId) : undefined) {
      if (nodeID.has(current.id)) return nodeID.get(current.id);
    }
    return undefined;
  };
  const lines = ['flowchart TD'];
  for (const issue of visible) {
    const state = factoryIssueState(issue);
    lines.push(`  ${nodeID.get(issue.id)}["${label(issue.title)}<br/>${label(issue.kind)} · ${STATE_LABEL[state]}"]:::${state}`);
  }
  for (const issue of visible) {
    const parent = visibleAncestor(issue.parentId);
    if (parent) lines.push(`  ${parent} -.-> ${nodeID.get(issue.id)}`);
    for (const edge of issue.dependsOn ?? []) {
      const from = visibleAncestor(edge.id);
      if (!from || from === nodeID.get(issue.id)) continue;
      lines.push(edge.type === 'on_failure' ? `  ${from} -- on failure --> ${nodeID.get(issue.id)}` : `  ${from} --> ${nodeID.get(issue.id)}`);
    }
  }
  for (const [state, style] of Object.entries(STATE_STYLE)) lines.push(`  classDef ${state} ${style},color:#cdd6f4;`);
  return lines.join('\n');
}
