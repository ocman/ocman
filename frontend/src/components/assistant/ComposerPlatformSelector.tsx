import { useMachinePlatforms } from '../../lib/useCapabilities';
import { SearchSelect } from '../SearchSelect';

// Picks the platform a new conversation starts on. Hidden when the machine
// offers only one; the shared draft survives the switch like a machine change.
export function ComposerPlatformSelector({ remoteId = 'local', platform, disabled, onSelect }: {
  remoteId?: string;
  platform?: string;
  disabled?: boolean;
  onSelect: (platform: string) => void;
}) {
  const options = useMachinePlatforms(remoteId);
  if (options.length < 2) return null;
  return <span className="oc-composer-selectors oc-composer-selectors-left" data-testid="composer-platform-selector">
    <span className="oc-selector-icon" aria-hidden="true"><i className="bi bi-cpu" /></span>
    <span onClick={(event) => event.stopPropagation()}>
      <SearchSelect
        className="oc-bar-search"
        ariaLabel="Session platform"
        title="Start a new conversation on this platform"
        placeholder="Platform"
        searchLabel="Search platforms"
        value={platform ?? ''}
        disabled={disabled}
        options={options.map((p) => ({ value: p.id, label: p.displayName, disabled: !p.capabilities.composer }))}
        onChange={(value) => { if (value !== platform) onSelect(value); }}
      />
    </span>
  </span>;
}
