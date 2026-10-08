import type { FactoryPlanGate, FactoryProposal } from '../lib/api';
import { Button } from './Control';
import { MarkdownContent } from './assistant/MarkdownText';

export function FactoryProposalHistory({ proposals, gate, disabled, approving, onApprove }: {
  proposals: FactoryProposal[]; gate?: FactoryPlanGate; disabled: boolean; approving: boolean;
  onApprove: (proposal: FactoryProposal) => void;
}) {
  return <>
    {gate?.resolution === 'approved' && proposals.every((proposal) => proposal.revision <= gate.proposalRevision) && <p role="status">No new proposal is awaiting approval.</p>}
    {proposals.map((proposal) => {
      const current = proposal.revision === gate?.proposalRevision && proposal.contentHash === gate.proposalHash;
      const pending = current && gate?.resolution === 'open';
      return <details key={proposal.revision} className="factory-proposal" open={pending || undefined}>
        <summary>Proposal revision: {proposal.revision}</summary>
        {current && <p>{{ open: 'Awaiting approval', approved: 'Approved', revision_requested: 'Revision requested', rejected: 'Rejected' }[gate.resolution] ?? gate.resolution}</p>}
        {pending && <Button type="button" variant="accent" disabled={disabled} aria-busy={approving} onClick={() => onApprove(proposal)}>{approving ? `Approving revision ${proposal.revision}…` : `Approve revision ${proposal.revision}`}</Button>}
        <p>Content hash: {proposal.contentHash}</p><pre>{JSON.stringify(proposal.manifest, null, 2)}</pre>
        {proposal.rationaleMarkdown && <div className="oc-md"><MarkdownContent text={proposal.rationaleMarkdown} factoryCards={false} /></div>}
      </details>;
    })}
  </>;
}
