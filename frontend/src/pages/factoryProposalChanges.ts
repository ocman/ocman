import type { FactoryIssue, FactoryProposal } from '../lib/api';
import { factoryGraphModel, proposalIssues, type GraphEdge } from './factoryGraph';

export interface ProposalChanges {
  addedIssues: Set<string>;
  addedEdges: Set<string>;
}

export function proposalBaseline(proposal: FactoryProposal, history: FactoryProposal[]) {
  if (proposal.manifest.baseRevision !== undefined) return history.find((item) => item.revision === proposal.manifest.baseRevision);
  return history.filter((item) => item.revision < proposal.revision).sort((a, b) => b.revision - a.revision)[0];
}

// Manifest keys survive materialization into real Issue IDs. Compare those keys
// when the baseline is an initial plan; its Formula containers are not in nodes.
export function proposalChanges(manifest: FactoryProposal['manifest'], baseline?: FactoryProposal['manifest']): ProposalChanges {
  const current = proposalIssues(manifest);
  const previous = baseline ? proposalIssues(baseline) : [];
  const key = (issue: FactoryIssue) => issue.manifestKey || issue.id;
  const known = new Set(previous.map(key));
  const references = new Set((manifest.externalIssues ?? []).map((issue) => issue.id));
  const eligible = (issue: FactoryIssue) => baseline?.issues || (['task', 'implementation', 'delivery'].includes(issue.kind) && (!issue.workflow || issue.workflow.kind === 'implementation'));
  const addedIssues = new Set(current.filter((issue) => !references.has(issue.id) && issue.epicId === manifest.epicId && eligible(issue) && !known.has(key(issue))).map((issue) => issue.id));
  const before = new Map(previous.map((issue) => [issue.id, issue]));
  const after = new Map(current.map((issue) => [issue.id, issue]));
  const pair = (edge: GraphEdge, byID: Map<string, FactoryIssue>) => `${key(byID.get(edge.source)!)}\0${key(byID.get(edge.target)!)}\0${edge.kind}`;
  const oldEdges = new Set(factoryGraphModel(previous, true).edges.map((edge) => pair(edge, before)));
  const comparable = (id: string) => known.has(key(after.get(id)!)) || addedIssues.has(id) || references.has(id);
  const addedEdges = new Set(factoryGraphModel(current, true).edges.filter((edge) =>
    (baseline?.issues || (edge.kind !== 'hierarchy' && comparable(edge.source) && comparable(edge.target))) && !oldEdges.has(pair(edge, after)),
  ).map((edge) => edge.id));
  return { addedIssues, addedEdges };
}
