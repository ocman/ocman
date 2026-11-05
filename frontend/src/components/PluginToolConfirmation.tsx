import { useMutation, useQueryClient } from '@tanstack/react-query';
import { plugins, type PluginAgentTools, type PluginToolConfirmation } from '../lib/plugins';
import { Button, ButtonGroup } from './Control';
import { usePluginAgentTools } from '../lib/pluginAgentTools';

const STATUS: Record<PluginToolConfirmation['status'], string> = {
  pending: 'Waiting for your decision',
  approved: 'Approved. The agent can now repeat the call once.',
  denied: 'Denied',
  used: 'Approved and used',
  expired: 'Expired',
};

function useDecide(owner: string, pluginId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, decision }: { id: string; decision: 'approve' | 'deny' }) => plugins.decideToolConfirmation(owner, pluginId, id, decision),
    onSuccess: (report: PluginAgentTools) => client.setQueryData(['plugin-agent-tools', owner, pluginId], report),
    onSettled: () => client.invalidateQueries({ queryKey: ['plugin-agent-tools', owner, pluginId] }),
  });
}

/**
 * One agent request for consent. Only the user decides here; the agent sees
 * the outcome when it repeats the call. Arguments are shown as the host
 * masked them, never with write-only values.
 */
export function PluginToolConfirmationItem({ owner, pluginId, confirmation: c }: { owner: string; pluginId: string; confirmation: PluginToolConfirmation }) {
  const decide = useDecide(owner, pluginId);
  return <div aria-label={`Agent request ${c.id}`}>
    <p><strong>{c.text}</strong></p>
    <p>Tool <code>{c.tool}</code> from <code>{pluginId}</code>{owner !== 'local' ? <> on <code>{owner}</code></> : null}</p>
    <pre aria-label="Requested arguments">{JSON.stringify(c.arguments ?? {}, null, 2)}</pre>
    {c.status === 'pending'
      ? <ButtonGroup label={`Decide agent request ${c.id}`}>
        <Button type="button" size="small" variant="accent" disabled={decide.isPending} onClick={() => decide.mutate({ id: c.id, decision: 'approve' })}>Approve</Button>
        <Button type="button" size="small" variant="danger" disabled={decide.isPending} onClick={() => decide.mutate({ id: c.id, decision: 'deny' })}>Deny</Button>
      </ButtonGroup>
      : <p role="status">{STATUS[c.status]}</p>}
    {decide.isError && <p role="alert">Could not record the decision. It may have expired or the plugin changed; ask the agent to request it again.</p>}
  </div>;
}

/** Rendered under an agent's tool call that asked for the user's consent. */
export function PluginToolConfirmationCard({ owner, pluginId, confirmationId }: { owner: string; pluginId: string; confirmationId: string }) {
  const report = usePluginAgentTools(owner, pluginId);
  const confirmation = report.data?.confirmations.find((c) => c.id === confirmationId);
  return <div className="oc-epic-card" aria-label="Plugin tool confirmation">
    {report.isError && <span role="alert">Could not load this request. Review it in Settings → Plugins.</span>}
    {report.data && !confirmation && <span role="status">This request is no longer available. It expired or ocman restarted.</span>}
    {confirmation && <PluginToolConfirmationItem owner={owner} pluginId={pluginId} confirmation={confirmation} />}
  </div>;
}
