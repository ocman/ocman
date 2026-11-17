import { useId, useRef, useState } from 'react';
import { Button, SelectField } from '../components/Control';
import { InlineAlert } from '../components/InlineAlert';
import { useRemotes } from '../lib/settingsQueries';

export function BackgroundTaskMachineSelect({ value, onChange }: { value: string; onChange: (owner: string) => void }) {
  const remotes = useRemotes();
  const [open, setOpen] = useState(false);
  const id = useId();
  const toggle = useRef<HTMLButtonElement>(null);
  const machines = new Map([['local', { name: 'This machine', connected: true }]]);
  for (const remote of remotes.data ?? []) {
    if (!remote.remoteId || machines.has(remote.remoteId)) continue;
    machines.set(remote.remoteId, { name: remote.displayName || remote.hostname || remote.remoteId,
      connected: remote.enabled && remote.health === 'connected' });
  }
  if (!machines.has(value)) machines.set(value, { name: value, connected: false });
  if (machines.size === 1 && !remotes.isError) return null;
  const close = () => { setOpen(false); toggle.current?.focus(); };
  return <div className="background-task-machine" onKeyDown={event => {
    if (event.key === 'Escape') { event.stopPropagation(); close(); }
  }}>
    <Button ref={toggle} variant="ghost" aria-label={`Task machine: ${machines.get(value)!.name}`}
      aria-expanded={open} aria-controls={open ? id : undefined} onClick={() => setOpen(!open)}>
      <i className="bi bi-pc-display" aria-hidden="true" />{machines.get(value)!.name}
      <i className="bi bi-chevron-down" aria-hidden="true" />
    </Button>
    {open && <div id={id} className="background-task-machine-menu">
      <SelectField autoFocus aria-label="Task machine" value={value} onChange={event => { onChange(event.target.value); close(); }}>
        {Array.from(machines, ([owner, machine]) => <option key={owner} value={owner}>
          {machine.name}{machine.connected ? '' : ' (offline)'}
        </option>)}
      </SelectField>
      {remotes.isError && <InlineAlert onRetry={() => void remotes.refetch()} retrying={remotes.isFetching}>Could not load machines.</InlineAlert>}
    </div>}
  </div>;
}
