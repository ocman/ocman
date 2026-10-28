import { useEffect, useState } from 'react';
import { formatExactDate, relativeTimeISO } from '../lib/format';
import { Tooltip } from './Tooltip';
import './RelativeTime.css';

const SECOND_MS = 1_000;
const MINUTE_MS = 60_000;

// RelativeTime renders an ISO-8601 timestamp as "12s ago" / "3h ago"
// and keeps counting: a plain relativeTimeISO() call is only correct
// at render time, so a card that isn't re-rendered freezes at whatever
// it said when it mounted.
//
// The tick matches the resolution actually on screen — once a second
// while the label is still counting seconds, once a minute after that.
// No timer at all for an unparseable timestamp (the label is empty and
// will never change).
export function RelativeTime({ iso }: { iso: string }) {
  const [, setTick] = useState(0);
  const parsed = Date.parse(iso);

  // Self-rescheduling timeout rather than a fixed interval: the clock
  // is read in the effect (never during render), and the delay follows
  // the label as it ages out of seconds into minutes.
  useEffect(() => {
    if (Number.isNaN(parsed)) return;
    let id: ReturnType<typeof setTimeout>;
    const schedule = () => {
      const period = Date.now() - parsed < MINUTE_MS ? SECOND_MS : MINUTE_MS;
      id = setTimeout(() => {
        setTick((n) => n + 1);
        schedule();
      }, period);
    };
    schedule();
    return () => clearTimeout(id);
  }, [parsed]);

  return <>{relativeTimeISO(iso)}</>;
}

/**
 * Recent activity: relative text that keeps counting, with the shared exact
 * date on hover and keyboard focus.
 */
export function RelativeTimestamp({ value }: { value: string | number | Date }) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  const iso = date.toISOString();
  const exact = formatExactDate(date);
  return <Tooltip content={exact}>
    {/* The accessible name comes from the content, so it ticks with the text. */}
    <time dateTime={iso} tabIndex={0}><RelativeTime iso={iso} /><span className="oc-time-exact">, {exact}</span></time>
  </Tooltip>;
}
