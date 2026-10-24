// Plan graph thumbnail for inline Factory cards. The thumbnail is plain SVG so it
// stays phrasing content inside a markdown paragraph. Nodes open task details;
// the expand button opens the full epic graph with pan and zoom.
import { useState } from 'react';
import { createPortal } from 'react-dom';
import { EpicGraph } from '../pages/EpicGraph';
import { IssueDrawer } from '../pages/FactoryIssues';
import { factoryGraphModel } from '../pages/factoryGraph';
import { FactoryGraphSvg } from '../pages/FactoryGraphSvg';
import type { FactoryIssue } from '../lib/api';
import { Modal } from './Modal';
import { Button } from './Control';
import './FactoryPlanGraph.css';

export function FactoryPlanGraph({ issues }: { issues: FactoryIssue[] }) {
  const [expanded, setExpanded] = useState(false);
  const [selected, setSelected] = useState<FactoryIssue>();
  const { nodes, edges } = factoryGraphModel(issues, true);
  if (!nodes.length) return null;
  return <>
    <span className="oc-factory-plan-graph">
      <FactoryGraphSvg nodes={nodes} edges={edges} onSelect={setSelected} />
      <Button variant="ghost" aria-label="Expand plan graph" onClick={() => setExpanded(true)}>Expand graph</Button>
    </span>
    {selected && createPortal(<IssueDrawer issue={selected} preview onClose={() => setSelected(undefined)} />, document.body)}
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
