import { useMemo } from 'react';
import type { SlashCommand } from '../../lib/api';
import { CommandListPicker, type PickerEntryBase } from './CommandListPicker';
import './ModelPicker.css';

export interface SkillPickerProps {
  open: boolean;
  /** Full slash-command catalog; skills are filtered out by source. */
  commands: SlashCommand[];
  initialQuery?: string;
  /** Called with the bare skill name (no leading slash). */
  onSelect: (skill: string) => void;
  onClose: () => void;
}

interface PickerEntry extends PickerEntryBase {
  value: string;
  label: string;
  description: string;
}

// Skills in OpenCode are surfaced as slash commands with source === "skill"
// (from GET /command). The /skills picker is a discovery helper: it lists
// them and, on select, prefills `/<skill> ` in the composer — it does not
// send, matching OpenCode's DialogSkill.
export function SkillPicker({ open, commands, initialQuery, onSelect, onClose }: SkillPickerProps) {
  const entries = useMemo<PickerEntry[]>(
    () =>
      commands
        .filter((c) => c.source === 'skill')
        .map((c) => ({ value: c.name, label: c.name, description: c.description ?? '' })),
    [commands],
  );

  const renderRow = (e: PickerEntry) => (
    <div className="oc-cmd-item-content">
      <span className="oc-cmd-title">{e.label}</span>
      {e.description && <span className="oc-cmd-meta">{e.description}</span>}
    </div>
  );

  return (
    <CommandListPicker<PickerEntry>
      open={open}
      entries={entries}
      fuseKeys={[
        { name: 'label', weight: 1.0 },
        { name: 'description', weight: 0.4 },
      ]}
      renderRow={renderRow}
      placeholder={(total) => (total > 0 ? `Select a skill (${total} available)...` : 'Select a skill...')}
      emptyMessage="No skills found"
      isCurrent={() => false}
      initialQuery={initialQuery}
      onSelect={onSelect}
      onClose={onClose}
    />
  );
}
