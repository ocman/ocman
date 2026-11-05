import { useQuery } from '@tanstack/react-query';
import { plugins, type PluginAgentTools } from './plugins';

/**
 * How often to refresh an agent tools report. Requests still awaiting a
 * decision or a single use refresh quickly. Settings keeps polling slowly so
 * new requests appear while it stays open; a card under a finished request stops.
 */
export function agentToolsRefetchInterval(report: PluginAgentTools | undefined, always: boolean): number | false {
  const active = report?.confirmations.some((c) => c.status === 'pending' || c.status === 'approved');
  if (active) return 3000;
  return always ? 10000 : false;
}

/** The owner's agent tools report, refreshed while it can still change. */
export function usePluginAgentTools(owner: string, pluginId: string, always = false) {
  return useQuery({
    queryKey: ['plugin-agent-tools', owner, pluginId],
    queryFn: ({ signal }) => plugins.agentTools(owner, pluginId, signal),
    refetchInterval: (query) => agentToolsRefetchInterval(query.state.data, always),
  });
}

const CARD = /\[\[ocman:card type=plugin-tool-confirmation plugin=([a-z0-9.-]{1,253}) confirmation=(tc_[A-Za-z0-9]{1,64})\]\]/;

/** The host-issued confirmation marker in a tool result, if any. */
export function pluginToolConfirmationFromOutput(output: string): { pluginId: string; confirmationId: string } | null {
  const match = CARD.exec(output);
  return match ? { pluginId: match[1], confirmationId: match[2] } : null;
}
