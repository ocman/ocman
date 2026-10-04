import { useEffect, useState } from 'react';
import { api } from '../../lib/api';
import type { ResolveTargetsResponse, TargetCandidate } from '../../lib/api.types';
import { SearchSelect } from '../SearchSelect';

export function ComposerMachineSelector({ directory, remoteId = 'local', disabled, onSelect }: {
  directory: string;
  remoteId?: string;
  disabled?: boolean;
  onSelect: (target: TargetCandidate) => Promise<void>;
}) {
  const [targets, setTargets] = useState<ResolveTargetsResponse | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    let ignore = false;
    const refresh = () => {
      api.resolveTargets(directory, remoteId).then((result) => {
        if (!ignore) { setTargets(result); setError(''); }
      }).catch(() => {
        if (!ignore) setError('Could not load machines');
      });
    };
    refresh();
    window.addEventListener('focus', refresh);
    return () => { ignore = true; window.removeEventListener('focus', refresh); };
  }, [directory, remoteId]);

  const remotes = targets?.remotes ?? [];
  if (remotes.length === 0 && !error) return null;
  const candidates = targets?.candidates ?? [];
  const machines = [{ remoteId: 'local', remoteName: 'This machine' }, ...remotes];
  if (!machines.some((m) => m.remoteId === remoteId)) {
    machines.push({ remoteId, remoteName: 'Current machine' });
  }
  return <span className="oc-composer-selectors oc-composer-selectors-left" data-testid="composer-machine-selector">
    <span className="oc-selector-icon" aria-hidden="true"><i className="bi bi-pc-display" /></span>
    <span onClick={(event) => event.stopPropagation()}>
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
            disabled: !current && !matched,
          };
        })}
        onChange={(value) => {
          const target = candidates.find((c) => c.remoteId === value);
          if (!target || target.remoteId === remoteId) return;
          setError('');
          void onSelect(target).catch(() => setError('Could not start a session on that machine'));
        }}
      />
    </span>
    {error && <span role="alert">{error}</span>}
  </span>;
}
