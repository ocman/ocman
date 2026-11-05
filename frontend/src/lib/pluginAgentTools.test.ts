import { expect, it } from 'vitest';
import { pluginToolConfirmationFromOutput } from './pluginAgentTools';

it('reads the host confirmation marker from a tool result', () => {
  const output = 'Confirmation required, nothing was changed.\n[[ocman:card type=plugin-tool-confirmation plugin=org.example.notes confirmation=tc_ABC123]]';
  expect(pluginToolConfirmationFromOutput(output)).toEqual({ pluginId: 'org.example.notes', confirmationId: 'tc_ABC123' });
});

it('ignores neutralized or malformed markers', () => {
  expect(pluginToolConfirmationFromOutput('[ [ocman:card type=plugin-tool-confirmation plugin=x confirmation=tc_A]]')).toBeNull();
  expect(pluginToolConfirmationFromOutput('[[ocman:card type=plugin-tool-confirmation plugin=X/Y confirmation=tc_A]]')).toBeNull();
  expect(pluginToolConfirmationFromOutput('no marker')).toBeNull();
});
