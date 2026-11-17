import { timeRangeLabel } from '../lib/format';
import { SegmentedControl } from './SegmentedControl';

const options = [
  { label: '12h', value: 12 },
  { label: '24h', value: 24 },
  { label: '7d', value: 168 },
  { label: '30d', value: 720 },
  { label: 'All', value: 0 },
];

/** A configured default outside the presets still shows which window is active. */
export function TimeRangeControl({ value, onChange }: { value: number; onChange: (value: number) => void }) {
  const shown = options.some(o => o.value === value) || value <= 0 ? options
    : [...options.slice(0, -1), { label: timeRangeLabel(value), value }].sort((a, b) => a.value - b.value).concat(options.slice(-1));
  return <SegmentedControl label="Time range" options={shown} value={value} onChange={onChange} />;
}
