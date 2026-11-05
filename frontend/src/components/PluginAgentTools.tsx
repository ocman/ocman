import { hasAgentTools, type PluginRegistration, type PluginToolStatus } from '../lib/plugins';
import { SettingRow } from './SettingRow';
import { PluginToolConfirmationItem } from './PluginToolConfirmation';
import { usePluginAgentTools } from '../lib/pluginAgentTools';

const STATUS: Record<PluginToolStatus, string> = {
  available: 'Available to agents',
  disabled: 'Plugin disabled',
  stopped: 'Plugin not running',
  'not-granted': 'Grant not approved',
  conflict: 'Name conflict: withheld',
};

/** Names the tools an approval of tool.agent exposes, in the grant review. */
export function PluginAgentToolsDisclosure({ plugin, owner }: { plugin: PluginRegistration; owner: string }) {
  if (!hasAgentTools(plugin)) return null;
  return <p>With <code>tool.agent</code>, agents connected to ocman on {owner} can call these tools: {plugin.description.tools?.map((tool) => tool.name).join(', ')}.</p>;
}

/** Declared agent tools, their availability, and requests awaiting consent. */
export function PluginAgentTools({ plugin, owner }: { plugin: PluginRegistration; owner: string }) {
  if (!hasAgentTools(plugin)) return null;
  return <PluginAgentToolsSection plugin={plugin} owner={owner} />;
}

function PluginAgentToolsSection({ plugin, owner }: { plugin: PluginRegistration; owner: string }) {
  const id = plugin.description.id;
  const report = usePluginAgentTools(owner, id, true);
  const confirmations = report.data?.confirmations ?? [];
  return <fieldset style={{ border: 0, padding: 0 }}>
    <legend>Agent tools</legend>
    <SettingRow label="MCP tools" desc="Agents connected to this machine's ocman MCP endpoint can call available tools. Hints such as read-only are not permissions." block>
      {report.isError && <p role="alert">Could not load agent tools for this owner.</p>}
      {!report.data && !report.isError && <span role="status">Loading…</span>}
      {report.data && <ul>
        {report.data.tools.map((tool) => <li key={tool.name}>
          <code>{tool.name}</code>{tool.title ? ` · ${tool.title}` : ''} · {STATUS[tool.status]}
          {tool.requiredGrants?.length ? ` · needs ${tool.requiredGrants.join(', ')}` : ''}
          {tool.confirmation ? ' · always asks you first' : ''}
        </li>)}
      </ul>}
    </SettingRow>
    {confirmations.length > 0 && <SettingRow label="Agent requests" desc="Approve only requests you expect. An approval allows one identical call within ten minutes." block>
      <ul>{confirmations.map((c) => <li key={c.id}><PluginToolConfirmationItem owner={owner} pluginId={id} confirmation={c} /></li>)}</ul>
    </SettingRow>}
  </fieldset>;
}
