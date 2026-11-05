import { useQuery } from '@tanstack/react-query';
import { plugins } from './plugins';

/** The owner's agent tools report, polled while a request awaits a decision. */
export function usePluginAgentTools(owner: string, pluginId: string) {
  return useQuery({
    queryKey: ['plugin-agent-tools', owner, pluginId],
    queryFn: ({ signal }) => plugins.agentTools(owner, pluginId, signal),
    refetchInterval: (query) => (query.state.data?.confirmations.some((c) => c.status === 'pending') ? 3000 : false),
  });
}

const CARD = /\[\[ocman:card type=plugin-tool-confirmation plugin=([a-z0-9.-]{1,253}) confirmation=(tc_[A-Za-z0-9]{1,64})\]\]/;

/** The host-issued confirmation marker in a tool result, if any. */
export function pluginToolConfirmationFromOutput(output: string): { pluginId: string; confirmationId: string } | null {
  const match = CARD.exec(output);
  return match ? { pluginId: match[1], confirmationId: match[2] } : null;
}
