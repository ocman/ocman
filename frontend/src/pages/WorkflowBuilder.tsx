import { useEffect, useState } from 'react';
import { addEdge, Background, Controls, Handle, Position, ReactFlow, type Connection, type Edge, type Node, type NodeProps, useEdgesState, useNodesState } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import type { WorkflowDefinition } from '../lib/api';
import type { WorkflowNodeDefinition } from '../lib/api.types';
import { Button } from '../components/Control';

type FlowNode = Node<{ node: WorkflowNodeDefinition }, 'workflow'>;
const nodeTypes = { workflow: WorkflowNode };
const nodeTypesList: WorkflowNodeDefinition['type'][] = ['approval', 'agent', 'command', 'subworkflow', 'map', 'join'];

function flowNodes(definition: WorkflowDefinition): FlowNode[] {
	return definition.nodes.map((node, index) => ({ id: node.id, type: 'workflow', position: { x: 40 + (index % 3) * 230, y: 40 + Math.floor(index / 3) * 150 }, data: { node } }));
}

function flowEdges(definition: WorkflowDefinition): Edge[] {
	return (definition.dependencies ?? []).map((dependency) => ({ id: `${dependency.from}-${dependency.to}`, source: dependency.from, target: dependency.to }));
}

function WorkflowNode({ data, selected }: NodeProps<FlowNode>) {
	return <div className="workflow-canvas-node" data-selected={selected || undefined} data-type={data.node.type}><Handle type="target" position={Position.Top} /><small>{data.node.type}</small><strong>{data.node.name}</strong><span>{data.node.id}</span><Handle type="source" position={Position.Bottom} /></div>;
}

function defaults(type: WorkflowNodeDefinition['type'], id: string): WorkflowNodeDefinition {
	const name = type.replace('-', ' ').replace(/\b\w/g, (letter: string) => letter.toUpperCase());
	if (type === 'command') return { id, name, type, command: [''] };
	if (type === 'agent') return { id, name, type, agent: { directory: '', prompt: '' } };
	if (type === 'subworkflow') return { id, name, type, subworkflow: { workflowId: '' } };
	if (type === 'map') return { id, name, type, map: { source: '', key: '', join: '', subworkflow: { workflowId: '' } } };
	if (type === 'join') return { id, name, type, join: { policy: 'all-success' } };
	return { id, name, type };
}

export function WorkflowBuilder({ definition, source, onChange, onSourceChange }: { definition: WorkflowDefinition; source: string; onChange: (definition: WorkflowDefinition) => void; onSourceChange: (source: string) => void }) {
	const [nodes, setNodes, onNodesChange] = useNodesState<FlowNode>(flowNodes(definition));
	const [edges, setEdges, onEdgesChange] = useEdgesState(flowEdges(definition));
	const [selectedID, setSelectedID] = useState<string>();
	const [addOpen, setAddOpen] = useState(false);
	const [view, setView] = useState<'graph' | 'yaml'>('graph');
	const selected = definition.nodes.find((node) => node.id === selectedID);

	useEffect(() => {
		setNodes(flowNodes(definition));
		setEdges(flowEdges(definition));
		setSelectedID((id) => definition.nodes.some((node) => node.id === id) ? id : undefined);
	}, [definition, setEdges, setNodes]);

	function update(node: WorkflowNodeDefinition) {
		onChange({ ...definition, nodes: definition.nodes.map((current) => current.id === node.id ? node : current) });
	}

	function add(type: WorkflowNodeDefinition['type']) {
		const base = type.replace('-', '_');
		let suffix = 1;
		while (definition.nodes.some((node) => node.id === `${base}_${suffix}`)) suffix++;
		const node = defaults(type, `${base}_${suffix}`);
		onChange({ ...definition, nodes: [...definition.nodes, node] });
		setSelectedID(node.id);
		setAddOpen(false);
	}

	function remove(id: string) {
		onChange({ ...definition, nodes: definition.nodes.filter((node) => node.id !== id), dependencies: (definition.dependencies ?? []).filter((edge) => edge.from !== id && edge.to !== id) });
	}

	function rename(node: WorkflowNodeDefinition, id: string) {
		if (!id || definition.nodes.some((current) => current.id === id && current.id !== node.id)) return;
		onChange({ ...definition, nodes: definition.nodes.map((current) => current.id === node.id ? { ...current, id } : current), dependencies: (definition.dependencies ?? []).map((edge) => ({ ...edge, from: edge.from === node.id ? id : edge.from, to: edge.to === node.id ? id : edge.to })) });
		setSelectedID(id);
	}

	function connect(connection: Connection) {
		if (!connection.source || !connection.target || connection.source === connection.target || definition.dependencies.some((edge) => edge.from === connection.source && edge.to === connection.target)) return;
		setEdges((current) => addEdge(connection, current));
		onChange({ ...definition, dependencies: [...(definition.dependencies ?? []), { from: connection.source, to: connection.target }] });
	}

	return <section className="workflow-builder" aria-label="Workflow builder">
		<div className="workflow-builder-header"><div><span className="workflow-kicker">Visual builder</span><h3>Workflow editor</h3></div><div className="workflow-tabs" role="tablist" aria-label="Workflow editor view"><Button role="tab" variant="muted" aria-selected={view === 'graph'} onClick={() => setView('graph')}>Editor</Button><Button role="tab" variant="muted" aria-selected={view === 'yaml'} onClick={() => setView('yaml')}>YAML</Button></div></div>
		{view === 'graph' ? <div className="workflow-builder-layout">
			<div className="workflow-canvas" aria-label="Workflow canvas"><div className="workflow-add"><Button variant="accent" size="large" aria-expanded={addOpen} onClick={() => setAddOpen((open) => !open)}>+ Add</Button>{addOpen && <div role="menu" aria-label="Node types">{nodeTypesList.map((type) => <Button key={type} variant="muted" role="menuitem" onClick={() => add(type)}>{type}</Button>)}</div>}</div><ReactFlow nodes={nodes} edges={edges} nodeTypes={nodeTypes} onNodesChange={onNodesChange} onEdgesChange={onEdgesChange} onConnect={connect} onNodeClick={(_, node) => setSelectedID(node.id)} fitView><Background gap={16} /><Controls /></ReactFlow></div>
			<NodePanel node={selected} definition={definition} onDefinitionChange={onChange} onUpdate={update} onRename={rename} onDelete={remove} onClose={() => setSelectedID(undefined)} />
		</div> : <label className="workflow-yaml">Workflow YAML or JSON<textarea aria-label="Workflow YAML or JSON" value={source} onChange={(event) => onSourceChange(event.target.value)} spellCheck={false} /></label>}
	</section>;
}

function NodePanel({ node, definition, onDefinitionChange, onUpdate, onRename, onDelete, onClose }: { node?: WorkflowNodeDefinition; definition: WorkflowDefinition; onDefinitionChange: (definition: WorkflowDefinition) => void; onUpdate: (node: WorkflowNodeDefinition) => void; onRename: (node: WorkflowNodeDefinition, id: string) => void; onDelete: (id: string) => void; onClose: () => void }) {
	if (!node) {
		function updateTrigger(index: number, patch: Partial<WorkflowDefinition['triggers'][number]>) { onDefinitionChange({ ...definition, triggers: definition.triggers.map((trigger, current) => current === index ? { ...trigger, ...patch } : trigger) }); }
		function addTrigger() { const id = `trigger_${definition.triggers.length + 1}`; onDefinitionChange({ ...definition, triggers: [...definition.triggers, { id, type: 'manual' }] }); }
		return <aside className="workflow-node-panel" aria-label="Workflow properties"><div className="workflow-node-panel-title"><h3>Configure Workflow</h3></div><div className="workflow-node-summary"><span>Workflow settings</span><strong>{definition.name}</strong><small>Triggers and schedule</small></div><label>ID<input value={definition.id} onChange={(event) => onDefinitionChange({ ...definition, id: event.target.value })} /></label><label>Name<input value={definition.name} onChange={(event) => onDefinitionChange({ ...definition, name: event.target.value })} /></label><div className="workflow-trigger-editor"><h4>Triggers</h4><Button variant="muted" onClick={addTrigger}>Add trigger</Button>{definition.triggers.map((trigger, index) => <section key={trigger.id}><label>ID<input value={trigger.id} onChange={(event) => updateTrigger(index, { id: event.target.value })} /></label><label>Type<select value={trigger.type} onChange={(event) => updateTrigger(index, { type: event.target.value as typeof trigger.type })}><option value="manual">manual</option><option value="interval">interval</option><option value="cron">cron</option></select></label><label>Overlap policy<select value={trigger.overlap ?? 'skip'} onChange={(event) => updateTrigger(index, { overlap: event.target.value as 'skip' | 'queue' | 'parallel' })}><option value="skip">skip</option><option value="queue">queue</option><option value="parallel">parallel</option></select></label>{trigger.type === 'interval' && <label>Interval seconds<input type="number" min="1" value={trigger.intervalSeconds ?? ''} onChange={(event) => updateTrigger(index, { intervalSeconds: Number(event.target.value) || undefined })} /></label>}{trigger.type === 'cron' && <label>Cron schedule<input value={trigger.cron ?? ''} onChange={(event) => updateTrigger(index, { cron: event.target.value })} /></label>}<Button variant="muted" onClick={() => onDefinitionChange({ ...definition, triggers: definition.triggers.filter((_, current) => current !== index) })}>Remove trigger</Button></section>)}</div></aside>;
	}
	return <aside className="workflow-node-panel" aria-label="Node properties"><div className="workflow-node-panel-title"><h3>Configure Node</h3><button type="button" aria-label="Close node properties" onClick={onClose}>×</button></div><div className="workflow-node-summary"><span>{node.type} node</span><strong>{node.name}</strong><small>Workflow step configuration</small></div><label>ID<small>Lowercase letters, numbers, underscores, and hyphens only</small><input value={node.id} onChange={(event) => onRename(node, event.target.value)} /></label><label>Name<input value={node.name} onChange={(event) => onUpdate({ ...node, name: event.target.value })} /></label><label>Type<select value={node.type} onChange={(event) => onUpdate(defaults(event.target.value as WorkflowNodeDefinition['type'], node.id))}>{nodeTypesList.map((type) => <option key={type} value={type}>{type}</option>)}</select></label>{node.type === 'command' && <><h4>Command</h4><label>Command<input value={node.command?.join(' ') ?? ''} onChange={(event) => onUpdate({ ...node, command: event.target.value ? [event.target.value] : [] })} /></label></>}{node.type === 'agent' && <><h4>Agent configuration</h4><label>Directory<input value={node.agent?.directory ?? ''} onChange={(event) => onUpdate({ ...node, agent: { ...node.agent!, directory: event.target.value } })} /></label><label>Prompt<textarea value={node.agent?.prompt ?? ''} onChange={(event) => onUpdate({ ...node, agent: { ...node.agent!, prompt: event.target.value } })} /></label></>}{node.type === 'subworkflow' && <><h4>Subworkflow</h4><label>Workflow ID<input value={node.subworkflow?.workflowId ?? ''} onChange={(event) => onUpdate({ ...node, subworkflow: { workflowId: event.target.value } })} /></label></>}{node.type === 'map' && <><h4>Map configuration</h4><label>Items source<input value={node.map?.source ?? ''} onChange={(event) => onUpdate({ ...node, map: { ...node.map!, source: event.target.value } })} /></label><label>Item key<input value={node.map?.key ?? ''} onChange={(event) => onUpdate({ ...node, map: { ...node.map!, key: event.target.value } })} /></label><label>Subworkflow ID<input value={node.map?.subworkflow.workflowId ?? ''} onChange={(event) => onUpdate({ ...node, map: { ...node.map!, subworkflow: { workflowId: event.target.value } } })} /></label><label>Join node<input value={node.map?.join ?? ''} onChange={(event) => onUpdate({ ...node, map: { ...node.map!, join: event.target.value } })} /></label></>}{node.type === 'join' && <><h4>Join configuration</h4><label>Policy<select value={node.join?.policy ?? 'all-success'} onChange={(event) => onUpdate({ ...node, join: { policy: event.target.value as NonNullable<typeof node.join>['policy'] } })}><option value="all-success">all-success</option><option value="always">always</option><option value="minimum-success">minimum-success</option></select></label></>}<Button className="workflow-node-delete" variant="muted" onClick={() => onDelete(node.id)}>Delete node</Button></aside>;
}
