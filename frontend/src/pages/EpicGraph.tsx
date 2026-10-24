// Pannable, zoomable epic work graph. Read-only: nodes are positioned by
// factoryGraphModel and drawn as static SVG.
import { useMemo, useState } from 'react';
import { TransformComponent, TransformWrapper } from 'react-zoom-pan-pinch';
import { GRAPH_STATES, factoryGraphModel } from './factoryGraph';
import { FactoryGraphSvg } from './FactoryGraphSvg';
import { IssueDrawer } from './FactoryIssues';
import { EmptyState } from '../components/EmptyState';
import { IconButton } from '../components/IconButton';
import type { FactoryIssue } from '../lib/api';
import type { ProposalChanges } from './factoryProposalChanges';
import './EpicGraph.css';

// Preview nodes can be inspected before their issues have been created.
export function EpicGraph({ issues, preview, changes }: { issues?: FactoryIssue[]; preview?: boolean; changes?: ProposalChanges }) {
  // Keep the id, not the object, so the drawer follows live issue data.
  const [selectedID, setSelectedID] = useState<string>();
  const selected = issues?.find((issue) => issue.id === selectedID);
  const model = useMemo(() => factoryGraphModel(issues ?? [], preview), [issues, preview]);
  if (!model.nodes.length) return <EmptyState>This epic has no work to draw yet.</EmptyState>;
  return <div className="factory-graph">
    {changes && <p aria-label="Proposal additions">{changes.addedIssues.size} added issues · {changes.addedEdges.size} added connections</p>}
    <ul className="factory-graph-legend" aria-label="Status legend">{GRAPH_STATES.map((state) => <li key={state}><span className={`factory-graph-swatch ${state}`} aria-hidden="true" />{state}</li>)}</ul>
    <div className="factory-graph-canvas" data-testid="epic-graph">
      <TransformWrapper minScale={0.5} maxScale={8} centerOnInit centerZoomedOut doubleClick={{ disabled: true }}>
        {({ zoomIn, zoomOut, resetTransform }) => <>
          <TransformComponent wrapperStyle={{ width: '100%', height: '100%' }} contentStyle={{ display: 'flex', width: '100%', height: '100%' }}>
            <FactoryGraphSvg nodes={model.nodes} edges={model.edges} detailed changes={changes} onSelect={(issue) => setSelectedID(issues?.find((item) => item.id === issue.id)?.id)} />
          </TransformComponent>
          <div className="factory-graph-controls">
            <IconButton label="Zoom in" icon="bi-plus-lg" variant="ghost" onClick={() => zoomIn()} />
            <IconButton label="Zoom out" icon="bi-dash-lg" variant="ghost" onClick={() => zoomOut()} />
            <IconButton label="Fit view" icon="bi-fullscreen" variant="ghost" onClick={() => resetTransform()} />
          </div>
        </>}
      </TransformWrapper>
    </div>
    {selected && <IssueDrawer key={selected.id} issue={selected} preview={preview} onClose={() => setSelectedID(undefined)} />}
  </div>;
}
