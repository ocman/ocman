// Static SVG rendering of the shared Dagre layout. The compact form is the
// inline plan thumbnail; the detailed form adds node details, arrows and edge
// labels for the pannable epic graph.
import { useId, type KeyboardEvent } from 'react';
import { GRAPH_NODE_HEIGHT, GRAPH_NODE_WIDTH, graphEdgeGroups, type GraphEdge, type GraphNode } from './factoryGraph';
import type { FactoryIssue } from '../lib/api';
import type { ProposalChanges } from './factoryProposalChanges';

const PAD = 20;

function nodeDetail(issue: FactoryIssue) {
  if (issue.authority) return 'permission decision';
  if (issue.recovery) return 'recovery decision';
  if (issue.kind === 'phase') return 'phase completion';
  return issue.workflow?.kind ?? issue.kind;
}

function edgeLabel(item: GraphEdge, groupSize: number, changes?: ProposalChanges) {
  if (changes?.addedEdges.has(item.id)) return `Added ${item.kind === 'hierarchy' ? 'contains' : item.kind.replaceAll('_', ' ')}`;
  switch (item.kind) {
    case 'on_failure': return 'on failure';
    case 'merge_gated': return 'merge gated';
    case 'interrupts': return 'decision for task';
    case 'completion': return 'phase completion';
    case 'hierarchy': return 'contains';
    default: return groupSize > 1 ? 'blocks' : undefined;
  }
}

export function FactoryGraphSvg({ nodes, edges, detailed = false, changes, onSelect }: {
  nodes: GraphNode[];
  edges: GraphEdge[];
  detailed?: boolean;
  changes?: ProposalChanges;
  onSelect: (issue: FactoryIssue) => void;
}) {
  const markerID = useId();
  const at = new Map(nodes.map((node) => [node.id, node]));
  const minX = Math.min(...nodes.map((node) => node.x)) - PAD;
  const minY = Math.min(...nodes.map((node) => node.y)) - PAD;
  const width = Math.max(...nodes.map((node) => node.x)) + GRAPH_NODE_WIDTH + PAD - minX;
  const height = Math.max(...nodes.map((node) => node.y)) + GRAPH_NODE_HEIGHT + PAD - minY;
  const select = (issue: FactoryIssue) => (event: KeyboardEvent) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      onSelect(issue);
    }
  };
  // Like a fit-view, the detailed graph fills its viewport but never grows past 2x.
  return <svg className={detailed ? 'factory-graph-svg' : undefined} style={detailed ? { maxWidth: width * 2, maxHeight: height * 2 } : undefined} viewBox={`${minX} ${minY} ${width} ${height}`} role="group" aria-label={`Plan graph with ${nodes.length} steps`}>
    {detailed && <defs><marker id={markerID} viewBox="0 0 10 10" refX="10" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" /></marker></defs>}
    {graphEdgeGroups(edges).map((group) => {
      // Parallel paths and labels would overlap. Show every relationship in one
      // readable label while the model and additions retain all typed edges.
      const edge = detailed ? group.find((item) => ['blocks', 'on_failure', 'merge_gated', 'completion'].includes(item.kind)) ?? group[0] : group[0];
      const from = at.get(edge.source);
      const to = at.get(edge.target);
      if (!from || !to) return null;
      const x1 = from.x + GRAPH_NODE_WIDTH / 2;
      const y1 = from.y + GRAPH_NODE_HEIGHT;
      const x2 = to.x + GRAPH_NODE_WIDTH / 2;
      const y2 = to.y;
      if (!detailed) return <line key={edge.id} className={`oc-factory-plan-graph-edge ${edge.kind}`} x1={x1} y1={y1} x2={x2} y2={y2}><title>{group.map((item) => item.kind).join(', ')}</title></line>;
      const midY = (y1 + y2) / 2;
      const added = group.some((item) => changes?.addedEdges.has(item.id));
      const label = group.map((item) => edgeLabel(item, group.length, changes)).filter(Boolean).join(' · ');
      return <g key={edge.id} className={`factory-edge factory-edge--${edge.kind}${added ? ' factory-edge--added' : ''}`} data-testid={`epic-graph-edge-${edge.id}`}>
        <path d={`M${x1},${y1} C${x1},${midY} ${x2},${midY} ${x2},${y2}`} markerEnd={edge.kind === 'interrupts' || edge.kind === 'hierarchy' ? undefined : `url(#${markerID})`} />
        {label && <text x={(x1 + x2) / 2} y={midY}>{label}</text>}
      </g>;
    })}
    {nodes.map((node) => {
      const added = changes?.addedIssues.has(node.id);
      return detailed
        ? <g key={node.id} className="factory-graph-node" data-testid={`epic-graph-node-${node.id}`} role="button" tabIndex={0} aria-label={`Inspect ${node.issue.title}`} onClick={() => onSelect(node.issue)} onKeyDown={select(node.issue)}>
          <foreignObject x={node.x} y={node.y} width={GRAPH_NODE_WIDTH} height={GRAPH_NODE_HEIGHT}>
            <div className={`factory-node-box factory-node-box--${node.state}${added ? ' factory-node-box--added' : ''}`}>
              <span className="factory-node"><strong title={node.issue.title}>{node.issue.title}</strong><span>{nodeDetail(node.issue)} · {node.state}</span>{added && <span className="factory-graph-added-label">Added</span>}</span>
            </div>
          </foreignObject>
        </g>
        : <g key={node.id} className={`factory-node-box--${node.state}`} role="button" tabIndex={0} aria-label={`Inspect ${node.issue.title}`} onClick={() => onSelect(node.issue)} onKeyDown={select(node.issue)}>
          <title>{node.issue.title}</title>
          <rect x={node.x} y={node.y} width={GRAPH_NODE_WIDTH} height={GRAPH_NODE_HEIGHT} rx={8} />
          <text x={node.x + 12} y={node.y + GRAPH_NODE_HEIGHT / 2}>{node.issue.title.length > 22 ? `${node.issue.title.slice(0, 21)}…` : node.issue.title}</text>
        </g>;
    })}
  </svg>;
}
