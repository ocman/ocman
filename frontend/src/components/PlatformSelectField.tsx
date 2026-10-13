import type { ReactNode } from 'react';
import { useMachinePlatforms } from '../lib/useCapabilities';
import { SelectField } from './Control';

/**
 * Explicit platform choice for a create form (FR-2). Empty means the
 * owner's default platform. Hidden when the machine offers only one.
 */
export function PlatformSelectField({ remoteId = 'local', value, onChange, disabled, label, className }: {
  remoteId?: string;
  value: string;
  onChange: (platform: string) => void;
  disabled?: boolean;
  label: ReactNode;
  className?: string;
}) {
  const options = useMachinePlatforms(remoteId || 'local').filter((p) => p.capabilities.composer);
  if (options.length < 2) return null;
  return <label className={className}>
    {label}
    <SelectField aria-label="Platform" value={value} disabled={disabled} onChange={(event) => onChange(event.target.value)}>
      <option value="">Default platform</option>
      {options.map((p) => <option key={p.id} value={p.id}>{p.displayName}</option>)}
    </SelectField>
  </label>;
}
