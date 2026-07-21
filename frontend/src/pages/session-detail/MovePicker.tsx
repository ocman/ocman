import { useState } from 'react';
import { shortPath } from '../../lib/format';
import {
  CommandListPicker,
  type PickerEntryBase,
} from '../../components/assistant/CommandListPicker';
import { Modal } from '../../components/Modal';

const NEW_DIRECTORY = '__new_directory__';

interface MoveEntry extends PickerEntryBase {
  label: string;
  path: string;
  section: 'Current' | 'Other' | 'New';
}

export interface MovePickerProps {
  open: boolean;
  currentDirectory: string;
  directories: string[];
  onSelect: (directory: string) => void;
  onCustom: () => void;
  onClose: () => void;
}

export function MovePicker({
  open,
  currentDirectory,
  directories,
  onSelect,
  onCustom,
  onClose,
}: MovePickerProps) {
  const otherDirectories = [...new Set(directories)]
    .filter((directory) => directory && directory !== currentDirectory)
    .sort((a, b) => shortPath(a).localeCompare(shortPath(b)));
  const entries: MoveEntry[] = [
    { value: currentDirectory, label: shortPath(currentDirectory), path: currentDirectory, section: 'Current' },
    ...otherDirectories.map((directory): MoveEntry => ({
      value: directory,
      label: shortPath(directory),
      path: directory,
      section: 'Other',
    })),
    { value: NEW_DIRECTORY, label: 'Choose another directory...', path: '', section: 'New' },
  ];

  return (
    <CommandListPicker<MoveEntry>
      open={open}
      entries={entries}
      fuseKeys={[
        { name: 'label', weight: 1 },
        { name: 'path', weight: 0.8 },
      ]}
      sectionOf={(entry) => entry.section}
      sectionOrder={['Current', 'Other', 'New']}
      renderRow={(entry) => (
        <div className="oc-cmd-item-content">
          <span className="oc-cmd-title">{entry.label}</span>
        </div>
      )}
      placeholder={() => 'Search directories'}
      emptyMessage="No matching directories"
      isCurrent={(entry) => entry.value === currentDirectory}
      onSelect={(value) => {
        if (value === NEW_DIRECTORY) onCustom();
        else if (value !== currentDirectory) onSelect(value);
      }}
      onClose={onClose}
    />
  );
}

export interface MovePathDialogProps {
  onSelect: (directory: string) => void;
  onClose: () => void;
}

export function MovePathDialog({ onSelect, onClose }: MovePathDialogProps) {
  const [directory, setDirectory] = useState('');
  const submit = () => {
    const value = directory.trim();
    if (value) onSelect(value);
  };

  return (
    <Modal
      backdropClassName="oc-rename-backdrop"
      dialogClassName="oc-rename-dialog"
      label="Move Session"
      onClose={onClose}
    >
      <h3>Move Session</h3>
      <input
        className="oc-rename-input"
        type="text"
        value={directory}
        onChange={(event) => setDirectory(event.target.value)}
        placeholder="Project directory"
        aria-label="Project directory"
        autoFocus
        onKeyDown={(event) => {
          if (event.key === 'Enter') submit();
        }}
      />
      <div className="oc-rename-actions">
        <button className="oc-rename-btn oc-rename-btn-submit" onClick={submit}>Move</button>
        <button className="oc-rename-btn oc-rename-btn-cancel" onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  );
}
