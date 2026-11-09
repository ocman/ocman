import { useEffect, useMemo, useRef, useState } from 'react';
import { api } from '../../lib/api';
import type { ResolveTargetsResponse, TargetCandidate } from '../../lib/api.types';
import { SearchSelect } from '../SearchSelect';
import { useShortcut } from '../../lib/shortcutRegistry';
import { HostBadge } from '../HostBadge';
import { CloneProjectDialog } from './CloneProjectDialog';

export function ComposerMachineSelector({ directory, remoteId = 'local', source, disabled, onSelect }: {
  directory: string;
  remoteId?: string;
  source?: { directory: string; remoteId: string };
  disabled?: boolean;
  onSelect: (target: TargetCandidate) => Promise<void>;
}) {
  const [targets, setTargets] = useState<ResolveTargetsResponse | null>(null);
  const [error, setError] = useState('');
  const [cloneMachine, setCloneMachine] = useState<TargetCandidate>();
  const sourceDirectory = source?.directory || directory;
  const sourceRemote = source?.remoteId || remoteId;
  const selectorRef = useRef<HTMLSpanElement>(null);
  const hostShortcut = useMemo(() => ({
    id: 'composer.select-host',
    scope: 'composer' as const,
    description: 'Select host for new conversation',
    enabled: () => !disabled && !!targets && targets.remotes.length > 0,
    handler: () => selectorRef.current?.querySelector<HTMLButtonElement>('[role="combobox"]')?.click(),
  }), [disabled, targets]);
  useShortcut(hostShortcut);
  useEffect(() => {
    let ignore = false;
    const refresh = () => {
      api.resolveTargets(sourceDirectory, sourceRemote).then((result) => {
        if (!ignore) { setTargets(result); setError(''); }
      }).catch(() => {
        if (!ignore) setError('Could not load machines');
      });
    };
    refresh();
    window.addEventListener('focus', refresh);
    return () => { ignore = true; window.removeEventListener('focus', refresh); };
  }, [sourceDirectory, sourceRemote]);

  const remotes = targets?.remotes ?? [];
  if (remotes.length === 0 && !error) return null;
  const candidates = targets?.candidates ?? [];
  const machines = [{ remoteId: 'local', remoteName: 'This machine' }, ...remotes];
  if (!machines.some((m) => m.remoteId === remoteId)) {
    machines.push({ remoteId, remoteName: 'Current machine' });
  }
  return <span className="oc-composer-selectors oc-composer-selectors-left" data-testid="composer-machine-selector">
    <span ref={selectorRef} onClick={(event) => event.stopPropagation()}>
      <SearchSelect
        className="oc-bar-search"
        ariaLabel="Session machine"
        title="Start a new conversation on this machine"
        placeholder="Machine"
        searchLabel="Search machines"
        value={remoteId}
        disabled={disabled || !targets}
        options={machines.map((machine) => {
          const current = machine.remoteId === remoteId;
          const matched = candidates.some((c) => c.remoteId === machine.remoteId);
          return {
            value: machine.remoteId,
            label: `${machine.remoteName}${!current && !matched ? ' · no matching project' : ''}`,
            displayLabel: <>
              <HostBadge alwaysShow showIcon remoteId={machine.remoteId} remoteName={machine.remoteName} />
              {!current && !matched ? ' · no matching project' : ''}
            </>,
            disabled: disabled,
          };
        })}
        onChange={(value) => {
          const target = candidates.find((c) => c.remoteId === value);
          if (value === remoteId || disabled) return;
          if (!target) {
            const machine = machines.find((m) => m.remoteId === value);
            if (machine) setCloneMachine({ ...machine, platform: remotes.find((m) => m.remoteId === value)?.platform || '', dir: '' });
            return;
          }
          setError('');
          void onSelect(target).catch(() => setError('Could not start a session on that machine'));
        }}
      />
    </span>
    {error && <span role="alert">{error}</span>}
    {cloneMachine && <CloneProjectDialog machine={cloneMachine} suggestedURL={targets?.cloneUrl} onClose={() => setCloneMachine(undefined)} onSelect={onSelect} />}
  </span>;
}
