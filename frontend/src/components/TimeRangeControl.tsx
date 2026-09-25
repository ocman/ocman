import { SegmentedControl } from './SegmentedControl';

const options = [
  { label: '12h', value: 12 },
  { label: '24h', value: 24 },
  { label: '7d', value: 168 },
  { label: '30d', value: 720 },
  { label: 'All', value: 0 },
];

export function TimeRangeControl({ value, onChange }: { value: number; onChange: (value: number) => void }) {
  return <SegmentedControl label="Time range" options={options} value={value} onChange={onChange} />;
}
