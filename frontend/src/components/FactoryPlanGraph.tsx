// Plan graph thumbnail for inline Factory cards. The thumbnail is plain SVG so it
// stays phrasing content inside a markdown paragraph; clicking opens the full
// ReactFlow graph in a modal, which brings its own pan and zoom.
import { useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import { EpicGraph } from '../pages/EpicGraph';
import { GRAPH_NODE_HEIGHT, GRAPH_NODE_WIDTH, factoryGraphModel } from '../pages/factoryGraph';
import type { FactoryIssue } from '../lib/api';
import { Modal } from './Modal';
import './FactoryPlanGraph.css';

const PAD = 20;

export function FactoryPlanGraph({ issues }: { issues: FactoryIssue[] }) {
  const [expanded, setExpanded] = useState(false);
  const { nodes, edges } = useMemo(() => factoryGraphModel(issues), [issues]);
  if (!nodes.length) return null;
  const at = new Map(nodes.map((node) => [node.id, node]));
  const minX = Math.min(...nodes.map((node) => node.x)) - PAD;
  const minY = Math.min(...nodes.map((node) => node.y)) - PAD;
  const width = Math.max(...nodes.map((node) => node.x)) + GRAPH_NODE_WIDTH + PAD - minX;
  const height = Math.max(...nodes.map((node) => node.y)) + GRAPH_NODE_HEIGHT + PAD - minY;
  return <>
    <button type="button" className="oc-factory-plan-graph" aria-label="Expand plan graph" onClick={() => setExpanded(true)}>
      <svg viewBox={`${minX} ${minY} ${width} ${height}`} role="img" aria-label={`Plan graph with ${nodes.length} steps`}>
        {edges.map((edge) => {
          const from = at.get(edge.source);
          const to = at.get(edge.target);
          if (!from || !to) return null;
          return <line key={edge.id} className={`oc-factory-plan-graph-edge ${edge.kind}`} x1={from.x + GRAPH_NODE_WIDTH / 2} y1={from.y + GRAPH_NODE_HEIGHT} x2={to.x + GRAPH_NODE_WIDTH / 2} y2={to.y} />;
        })}
        {nodes.map((node) => <g key={node.id} className={`factory-node-box--${node.state}`}>
          <title>{node.issue.title}</title>
          <rect x={node.x} y={node.y} width={GRAPH_NODE_WIDTH} height={GRAPH_NODE_HEIGHT} rx={8} />
          <text x={node.x + 12} y={node.y + GRAPH_NODE_HEIGHT / 2}>{node.issue.title.length > 22 ? `${node.issue.title.slice(0, 21)}…` : node.issue.title}</text>
        </g>)}
      </svg>
    </button>
    {expanded && createPortal(
      <Modal label="Plan graph" onClose={() => setExpanded(false)} backdropClassName="oc-mermaid-modal-backdrop" dialogClassName="oc-mermaid-modal oc-factory-plan-graph-modal">
        <div className="oc-mermaid-modal-toolbar">
          <button type="button" aria-label="Close plan graph" title="Close plan graph" onClick={() => setExpanded(false)}><i className="bi bi-x-lg" aria-hidden="true" /></button>
        </div>
        <EpicGraph issues={issues} preview />
      </Modal>,
      document.body,
    )}
  </>;
}
