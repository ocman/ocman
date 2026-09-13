import { useMemo } from 'react';
import { CommandListPicker, type PickerEntryBase } from './CommandListPicker';
import './ReasoningPicker.css';

/** Sentinel value for the "use platform default" option. */
const DEFAULT_VALUE = '';
const DEFAULT_LABEL = 'default';

interface ReasoningEntry extends PickerEntryBase {
  label: string;
}

export interface ReasoningPickerProps {
  open: boolean;
  options: string[];
  current?: string;
  onSelect: (value: string) => void;
  onClose: () => void;
}

/**
 * Non-searching CommandListPicker for picking a reasoning / thinking-budget
 * level. The first row is always "default" — selecting it clears the
 * override so the platform's own default applies.
 */
export function ReasoningPicker({
  open,
  options,
  current,
  onSelect,
  onClose,
}: ReasoningPickerProps) {
  const entries = useMemo<ReasoningEntry[]>(
    () => [DEFAULT_VALUE, ...options].map((value) => ({ value, label: value || DEFAULT_LABEL })),
    [options],
  );

  if (!open || options.length === 0) return null;

  // The effective current value — empty string matches the "default" row.
  const effectiveCurrent = current || DEFAULT_VALUE;

  return (
    <CommandListPicker
      open
      searchable={false}
      dialogClassName="oc-reasoning-picker"
      entries={entries}
      renderRow={(e) => <span className="oc-cmd-title">{e.label}</span>}
      placeholder={() => 'Reasoning level'}
      emptyMessage="No reasoning levels"
      isCurrent={(e) => e.value === effectiveCurrent}
      onSelect={onSelect}
      onClose={onClose}
    />
  );
}
