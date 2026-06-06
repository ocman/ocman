import { useMemo } from 'react';
import type { AgentInfo } from '../../lib/api';
import { agentColor } from '../../lib/agentColor';
import { CommandListPicker, type PickerEntryBase } from './CommandListPicker';
import './ModelPicker.css';

export interface AgentPickerProps {
  open: boolean;
  // Known agent names (union of active + any hard-coded list the composer
  // uses). Always present so the picker works even before /agent returns.
  agentNames: string[];
  // Rich agent info from OpenCode's /agent endpoint. Optional — fields like
  // description and mode light up when available.
  agents?: AgentInfo[];
  currentAgent?: string;
  activeAgent?: string; // for the "Active" section + distinguishing current
  initialQuery?: string;
  onSelect: (agent: string) => void;
  onClose: () => void;
}

// Internal row shape used for rendering.
interface PickerEntry extends PickerEntryBase {
  value: string;         // agent name — what we send to onSelect
  label: string;
  description: string;
  mode: 'primary' | 'subagent' | 'all' | '';
  color: string;
  hidden: boolean;
  builtIn: boolean;
  isActive: boolean;     // matches activeAgent (session's current)
  isCurrent: boolean;    // matches currentAgent (selectedAgent || activeAgent)
}

// buildEntries merges the known-name list with rich AgentInfo data. The
// AgentInfo side wins on fields, and we tag isActive/isCurrent so the UI can
// render section + selection state without another pass.
function buildEntries(
  agentNames: string[],
  agents: AgentInfo[] | undefined,
  activeAgent: string | undefined,
  currentAgent: string | undefined,
): PickerEntry[] {
  const byName = new Map<string, AgentInfo>();
  for (const a of agents || []) byName.set(a.name, a);

  // Union of names so we show known-but-unregistered agents (e.g. builtins
  // like "architect" the user has seen in other sessions) alongside the live
  // /agent catalog. De-dupe by name.
  const names = Array.from(new Set([...agentNames, ...(agents || []).map((a) => a.name)]));

  return names.map((name) => {
    const info = byName.get(name);
    return {
      value: name,
      label: name,
      description: info?.description ?? '',
      mode: info?.mode ?? '',
      color: agentColor(name, agents),
      hidden: !!info?.hidden,
      builtIn: !!info?.builtIn,
      isActive: !!activeAgent && name === activeAgent,
      isCurrent: !!currentAgent && name === currentAgent,
    };
  });
}

// Sections mirror how OpenCode surfaces agents:
//   Active  → the session's current agent (pinned at top)
//   Primary → agents that can drive a conversation
//   Subagents → agents invoked by other agents (task, research, etc.)
//   Hidden → hidden: true entries, surfaced only in search
function sectionOf(e: PickerEntry): string {
  if (e.isActive) return 'Active';
  if (e.hidden) return 'Hidden';
  if (e.mode === 'subagent') return 'Subagents';
  return 'Primary';
}
const SECTION_ORDER = ['Active', 'Primary', 'Subagents', 'Hidden'];

// Command-palette–style modal for picking an agent. Styled alongside
// ModelPicker so the two feel interchangeable.
export function AgentPicker({
  open,
  agentNames,
  agents,
  currentAgent,
  activeAgent,
  initialQuery,
  onSelect,
  onClose,
}: AgentPickerProps) {
  const entries = useMemo<PickerEntry[]>(
    () => buildEntries(agentNames, agents, activeAgent, currentAgent),
    [agentNames, agents, activeAgent, currentAgent],
  );

  const renderRow = (e: PickerEntry) => (
    <div className="oc-cmd-item-content">
      <span className="oc-cmd-title">
        <span className="oc-agent-swatch" aria-hidden="true" style={{ background: e.color }} />
        {e.label}
        {e.isActive && (
          <span className="oc-model-picker-badge oc-model-picker-badge--star" title="Active in this session">
            <i className="bi bi-star-fill" />
          </span>
        )}
        {e.builtIn && (
          <span className="oc-model-picker-badge oc-model-picker-badge--default" title="Built-in agent">
            built-in
          </span>
        )}
        {e.mode === 'subagent' && (
          <span className="oc-model-picker-badge oc-model-picker-badge--archived" title="Subagent">
            subagent
          </span>
        )}
      </span>
      {e.description && <span className="oc-cmd-meta">{e.description}</span>}
    </div>
  );

  return (
    <CommandListPicker<PickerEntry>
      open={open}
      entries={entries}
      // Agent name gets the most weight so "arch" matches "architect" before
      // a description that merely contains "arch".
      fuseKeys={[
        { name: 'label', weight: 1.0 },
        { name: 'description', weight: 0.4 },
        { name: 'mode', weight: 0.2 },
      ]}
      sectionOf={sectionOf}
      sectionOrder={SECTION_ORDER}
      renderRow={renderRow}
      placeholder={(total) => total > 0 ? `Select an agent (${total} available)...` : 'Select an agent...'}
      emptyMessage="No agents found"
      isCurrent={(e) => e.isCurrent}
      initialQuery={initialQuery}
      onSelect={onSelect}
      onClose={onClose}
    />
  );
}
