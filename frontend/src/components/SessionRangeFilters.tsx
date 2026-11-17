import { Button, ButtonGroup } from './Control';
import { TimeRangeControl } from './TimeRangeControl';

interface Props {
  label: string;
  className?: string;
  timeRange: number;
  onTimeRangeChange: (hours: number) => void;
  showArchived: boolean;
  onShowArchivedChange: (show: boolean) => void;
}

/** The one time-window + archived toggle used by every session list. */
export function SessionRangeFilters({ label, className, timeRange, onTimeRangeChange, showArchived, onShowArchivedChange }: Props) {
  return <ButtonGroup label={label} className={className}>
    <TimeRangeControl value={timeRange} onChange={onTimeRangeChange} />
    <Button type="button" size="small" variant={showArchived ? 'accent' : 'default'} aria-pressed={showArchived}
      onClick={() => onShowArchivedChange(!showArchived)}>Show archived</Button>
  </ButtonGroup>;
}
