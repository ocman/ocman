import { useEffect, useState } from 'react';
import { formatFullDateTime } from '../../lib/format';
import { formatTimelineMarker } from '../../lib/conversationTimeline';
import { Tooltip } from '../Tooltip';

export function TimelineMarker({ timestamp, testId }: { timestamp: number; testId?: string }) {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const next = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1);
    const timer = setTimeout(() => setNow(new Date()), next.getTime() - now.getTime());
    return () => clearTimeout(timer);
  }, [now]);
  return <div className="oc-timeline-marker" data-testid={testId}>
    <hr />
    <Tooltip content={formatFullDateTime(timestamp)}>
      <time dateTime={new Date(timestamp).toISOString()}>{formatTimelineMarker(timestamp, now)}</time>
    </Tooltip>
  </div>;
}
