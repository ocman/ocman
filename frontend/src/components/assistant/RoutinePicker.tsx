import { useEffect, useState } from 'react';
import { api, type Routine } from '../../lib/api';
import { CommandListPicker, type PickerEntryBase } from './CommandListPicker';
import './ModelPicker.css';

type Entry = PickerEntryBase & { routine: Routine; name: string; prompt: string };

export function RoutinePicker({ open, initialQuery, onSelect, onClose }: {
  open: boolean;
  initialQuery?: string;
  onSelect: (routine: Routine) => void;
  onClose: () => void;
}) {
  const [entries, setEntries] = useState<Entry[]>([]);
  const [message, setMessage] = useState('Loading routines...');

  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    api.routines.list(controller.signal)
      .then((items) => { setEntries(items.map((routine) => ({ value: routine.id, name: routine.name, prompt: routine.prompt, routine }))); setMessage('No routines found'); })
      .catch((err: Error) => { if (err.name !== 'AbortError') setMessage(err.message || 'Could not load routines'); });
    return () => controller.abort();
  }, [open]);

  return <CommandListPicker<Entry>
    open={open}
    entries={entries}
    fuseKeys={[{ name: 'name', weight: 1 }, { name: 'prompt', weight: .4 }]}
    renderRow={(entry) => <div className="oc-cmd-item-content"><span className="oc-cmd-title">{entry.name}</span><span className="oc-cmd-meta">{entry.prompt}</span></div>}
    placeholder={(total) => total ? `Select a routine (${total} available)...` : 'Select a routine...'}
    emptyMessage={message}
    isCurrent={() => false}
    initialQuery={initialQuery}
    onSelect={(id) => { const entry = entries.find((item) => item.value === id); if (entry) onSelect(entry.routine); }}
    onClose={onClose}
  />;
}
