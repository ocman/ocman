// Pannable, zoomable epic work graph. Read-only: nodes are positioned by
// factoryGraphModel, so nothing here is draggable or connectable.
import { useMemo, useState } from 'react';
import { Background, Controls, MarkerType, Position, ReactFlow, type Edge, type Node } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { GRAPH_NODE_HEIGHT, GRAPH_NODE_WIDTH, GRAPH_STATES, factoryGraphModel } from './factoryGraph';
import { IssueDrawer } from './FactoryIssues';
import { EmptyState } from '../components/EmptyState';
import type { FactoryIssue } from '../lib/api';
import type { ProposalChanges } from './factoryProposalChanges';
import './EpicGraph.css';

// preview: nodes are proposal keys, not issues, so there is nothing to open on click.
export function EpicGraph({ issues, preview, changes }: { issues?: FactoryIssue[]; preview?: boolean; changes?: ProposalChanges }) {
  const [selected, setSelected] = useState<FactoryIssue>();
  const { nodes, edges } = useMemo(() => {
    const model = factoryGraphModel(issues ?? [], preview);
    const flowNodes: Node[] = model.nodes.map((node) => ({
      id: node.id,
      position: { x: node.x, y: node.y },
      data: { label: <span className="factory-node"><strong title={node.issue.title}>{node.issue.title}</strong><span>{node.issue.authority ? 'permission decision' : node.issue.recovery ? 'recovery decision' : node.issue.kind === 'phase' ? 'phase completion' : node.issue.workflow?.kind ?? node.issue.kind} · {node.state}</span>{changes?.addedIssues.has(node.id) && <span className="factory-graph-added-label">Added</span>}</span> },
      style: { width: GRAPH_NODE_WIDTH, height: GRAPH_NODE_HEIGHT },
      className: `factory-node-box factory-node-box--${node.state}${changes?.addedIssues.has(node.id) ? ' factory-node-box--added' : ''}`,
      sourcePosition: Position.Bottom,
      targetPosition: Position.Top,
      connectable: false,
    }));
    const flowEdges: Edge[] = model.edges.map((edge) => ({
      id: edge.id,
      source: edge.source,
      target: edge.target,
      label: changes?.addedEdges.has(edge.id) ? `Added ${edge.kind === 'hierarchy' ? 'contains' : edge.kind.replaceAll('_', ' ')}` : edge.kind === 'on_failure' ? 'on failure' : edge.kind === 'merge_gated' ? 'merge gated' : edge.kind === 'interrupts' ? 'decision for task' : edge.kind === 'completion' ? 'phase completion' : edge.kind === 'hierarchy' ? 'contains' : undefined,
      animated: edge.kind === 'blocks' || edge.kind === 'merge_gated',
      className: `factory-edge factory-edge--${edge.kind}${changes?.addedEdges.has(edge.id) ? ' factory-edge--added' : ''}`,
      markerEnd: edge.kind === 'interrupts' || edge.kind === 'hierarchy' ? undefined : { type: MarkerType.ArrowClosed },
    }));
    return { nodes: flowNodes, edges: flowEdges };
  }, [issues, preview, changes]);
  if (!nodes.length) return <EmptyState>This epic has no work to draw yet.</EmptyState>;
  const byID = new Map((issues ?? []).map((issue) => [issue.id, issue]));
  return <div className="factory-graph">
    {changes && <p aria-label="Proposal additions">{changes.addedIssues.size} added issues · {changes.addedEdges.size} added connections</p>}
    <ul className="factory-graph-legend" aria-label="Status legend">{GRAPH_STATES.map((state) => <li key={state}><span className={`factory-graph-swatch ${state}`} aria-hidden="true" />{state}</li>)}</ul>
    <div className="factory-graph-canvas" data-testid="epic-graph">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        fitView
        minZoom={0.2}
        nodesDraggable={false}
        nodesConnectable={false}
        edgesFocusable={false}
        proOptions={{ hideAttribution: true }}
        onNodeClick={preview ? undefined : (_event, node) => setSelected(byID.get(node.id))}
      >
        <Background />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
    {selected && <IssueDrawer key={selected.id} issue={selected} onClose={() => setSelected(undefined)} />}
  </div>;
}
