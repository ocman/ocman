import { expect, it } from 'vitest';
import { agentToolsRefetchInterval, pluginToolConfirmationFromOutput } from './pluginAgentTools';
import type { PluginAgentTools, PluginToolConfirmation } from './plugins';

it('reads the host confirmation marker from a tool result', () => {
  const output = 'Confirmation required, nothing was changed.\n[[ocman:card type=plugin-tool-confirmation plugin=org.example.notes confirmation=tc_ABC123]]';
  expect(pluginToolConfirmationFromOutput(output)).toEqual({ pluginId: 'org.example.notes', confirmationId: 'tc_ABC123' });
});

it('ignores neutralized or malformed markers', () => {
  expect(pluginToolConfirmationFromOutput('[ [ocman:card type=plugin-tool-confirmation plugin=x confirmation=tc_A]]')).toBeNull();
  expect(pluginToolConfirmationFromOutput('[[ocman:card type=plugin-tool-confirmation plugin=X/Y confirmation=tc_A]]')).toBeNull();
  expect(pluginToolConfirmationFromOutput('no marker')).toBeNull();
});

const report = (...statuses: PluginToolConfirmation['status'][]): PluginAgentTools => ({
  tools: [],
  confirmations: statuses.map((status, i) => ({ id: `tc_${i}`, pluginId: 'p', tool: 't', key: '', text: 'Run?', arguments: {}, status, createdAt: i, expiresAt: i })),
});

it('keeps refreshing while a request can still change', () => {
  expect(agentToolsRefetchInterval(report('pending'), false)).toBe(3000);
  expect(agentToolsRefetchInterval(report('approved'), false)).toBe(3000);
  expect(agentToolsRefetchInterval(report('used', 'pending'), true)).toBe(3000);
});

it('keeps Settings polling for new requests but lets settled cards stop', () => {
  expect(agentToolsRefetchInterval(undefined, true)).toBe(10000);
  expect(agentToolsRefetchInterval(report(), true)).toBe(10000);
  expect(agentToolsRefetchInterval(report('used', 'denied', 'expired'), true)).toBe(10000);
  expect(agentToolsRefetchInterval(report('used', 'denied', 'expired'), false)).toBe(false);
  expect(agentToolsRefetchInterval(undefined, false)).toBe(false);
});
