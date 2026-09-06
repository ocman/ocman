import { useState } from 'react';
import { usePermissionStats } from '../../lib/queries';
import { AnalyticsFilters } from './AnalyticsFilters';
import { useDashboard } from './context';
import { PermissionStatsSection } from './PermissionStatsSection';

export function PermissionsTab() {
  const { dirScope } = useDashboard();
  const [days, setDays] = useState(30);
  const statsQ = usePermissionStats({ days, dir: dirScope || undefined });
  return (
    <div className="metrics-page">
      <AnalyticsFilters days={days} onDaysChange={setDays} />
      {statsQ.error instanceof Error && <div className="oc-error-banner">{statsQ.error.message}</div>}
      {statsQ.isLoading && <div className="oc-list-loading"><div className="oc-spinner" />Loading permission analytics...</div>}
      {statsQ.data && <PermissionStatsSection stats={statsQ.data} />}
    </div>
  );
}
