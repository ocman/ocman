import type { FactoryIssue, FactoryProposal } from '../lib/api';

// Shapes a not-yet-materialized proposal as issues so the epic graph can draw
// it. Nodes without blockers are 'ready', the rest 'waiting'.
export function proposalIssues(manifest: FactoryProposal['manifest']): FactoryIssue[] {
  if (manifest.issues) return [...manifest.issues, ...(manifest.externalIssues ?? []).map((issue) => ({ ...issue, title: `${issue.epicId}: ${issue.title}` }))];
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
