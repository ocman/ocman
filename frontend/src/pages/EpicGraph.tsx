// Pannable, zoomable epic work graph. Read-only: nodes are positioned by
// factoryGraphModel, so nothing here is draggable or connectable.
import { useMemo, useState } from 'react';
import { Background, Controls, MarkerType, Position, ReactFlow, type Edge, type Node } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { GRAPH_STATES, factoryGraphModel } from './factoryGraph';
import { IssueDrawer } from './FactoryIssues';
import { Button } from '../components/Control';
import type { FactoryIssue } from '../lib/api';
import './EpicGraph.css';

// preview: nodes are proposal keys, not issues, so there is nothing to open on click.
export function EpicGraph({ issues, preview }: { issues?: FactoryIssue[]; preview?: boolean }) {
  const [selected, setSelected] = useState<FactoryIssue>();
	const [expanded, setExpanded] = useState<Set<string>>(new Set());
	const phases = (issues ?? []).filter((issue) => issue.kind === 'phase');
  const { nodes, edges } = useMemo(() => {
		const collapsed = new Set((issues ?? []).filter((issue) => issue.kind === 'phase' && !expanded.has(issue.id)).map((issue) => issue.id));
    const model = factoryGraphModel((issues ?? []).filter((issue) => !collapsed.has(issue.parentId ?? '')));
    const flowNodes: Node[] = model.nodes.map((node) => ({
      id: node.id,
      position: { x: node.x, y: node.y },
      data: { label: <span className="factory-node"><strong>{node.issue.title}</strong><span>{node.issue.workflow?.kind ?? node.issue.kind} · {node.state}</span></span> },
      className: `factory-node-box factory-node-box--${node.state}`,
      sourcePosition: Position.Bottom,
      targetPosition: Position.Top,
      connectable: false,
    }));
    const flowEdges: Edge[] = model.edges.map((edge) => ({
      id: edge.id,
      source: edge.source,
      target: edge.target,
      label: edge.kind === 'on_failure' ? 'on failure' : edge.kind === 'merge_gated' ? 'merge gated' : edge.kind === 'interrupts' ? 'needs you' : undefined,
      animated: edge.kind === 'blocks' || edge.kind === 'merge_gated',
      className: `factory-edge factory-edge--${edge.kind}`,
      markerEnd: { type: MarkerType.ArrowClosed },
    }));
    return { nodes: flowNodes, edges: flowEdges };
  }, [issues, expanded]);
  if (!nodes.length) return <p className="oc-empty">This epic has no work to draw yet.</p>;
  const byID = new Map((issues ?? []).map((issue) => [issue.id, issue]));
  return <div className="factory-graph">
		{!!phases.length && <div className="factory-inbox-actions">{phases.map((phase) => <Button key={phase.id} type="button" size="small" aria-expanded={expanded.has(phase.id)} onClick={() => setExpanded((current) => { const next = new Set(current); if (next.has(phase.id)) next.delete(phase.id); else next.add(phase.id); return next; })}>{expanded.has(phase.id) ? 'Hide' : 'Show'} tasks for {phase.title}</Button>)}</div>}
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
