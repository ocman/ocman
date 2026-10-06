import type { StateFilter } from '../../lib/upstreamApi';
import { SegmentedControl } from '../SegmentedControl';
import { CheckboxField } from '../CheckboxField';
import styles from './UpstreamFilters.module.css';

const options = [
  { value: 'open', label: 'open' },
  { value: 'closed', label: 'closed' },
  { value: 'all', label: 'all' },
] as const;

export function UpstreamFilters({ state, onStateChange, mine, onMineChange }: {
  state: StateFilter;
  onStateChange: (state: StateFilter) => void;
  mine: boolean;
  onMineChange: (mine: boolean) => void;
}) {
  return <div className={styles.filters} role="toolbar" aria-label="Upstream filters">
    <SegmentedControl label="State" options={options} value={state} onChange={onStateChange} />
    <span className={styles.mine}><CheckboxField label="Mine" checked={mine} onChange={(event) => onMineChange(event.target.checked)} data-testid="upstream-filter-mine" /></span>
  </div>;
}
