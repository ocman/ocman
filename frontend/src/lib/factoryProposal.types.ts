import type { FactoryIssue } from './api.types';

export interface FactoryProposal {
  manifest: {
    epicId: string;
    molId: string;
    project: string;
    nodes: { key: string; type: string; requirement: string; title?: string; description?: string; project?: string; dependsOn?: string[] }[];
    edges?: { from: string; to: string; type: 'blocks' | 'on_failure' | 'merge_gated' }[];
    issues?: FactoryIssue[];
    externalIssues?: FactoryIssue[];
  };
  revision: number;
  contentHash: string;
  rationaleMarkdown?: string;
}
