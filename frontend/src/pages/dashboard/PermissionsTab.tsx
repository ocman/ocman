import { useState } from 'react';
import { InlineAlert } from '../../components/InlineAlert';
import { usePermissionStats } from '../../lib/queries';
import { AnalyticsFilters } from './AnalyticsFilters';
import { useDashboard } from './context';
import { PermissionStatsSection } from './PermissionStatsSection';
import { ChartSkeletons } from './shared';

export function PermissionsTab() {
  const { dirScope } = useDashboard();
  const [days, setDays] = useState(30);
  const statsQ = usePermissionStats({ days, dir: dirScope || undefined });
  return (
    <div>
      <AnalyticsFilters days={days} onDaysChange={setDays} />
      {statsQ.error instanceof Error && <InlineAlert>{statsQ.error.message}</InlineAlert>}
      {statsQ.isLoading && !statsQ.data && <ChartSkeletons labels={['Loading permission approvals', 'Loading observed user wait']} />}
      {statsQ.data && <PermissionStatsSection stats={statsQ.data} />}
    </div>
  );
}
