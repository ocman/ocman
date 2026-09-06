import { Navigate, NavLink, useLocation, useParams } from 'react-router-dom';
import { usePageTitle } from '../../lib/headerContext';
import { ActivityTab } from './ActivityTab';
import { LogsTab } from './LogsTab';
import { ModelsTab } from './ModelsTab';
import { OverviewTab } from './OverviewTab';
import { PerformanceTab } from './PerformanceTab';
import { PermissionsTab } from './PermissionsTab';

const sections = [
  ['overview', 'Overview'],
  ['activity', 'Activity'],
  ['models', 'Models & Cost'],
  ['performance', 'Performance'],
  ['permissions', 'Permissions'],
  ['logs', 'Logs'],
] as const;

export function AnalyticsTab() {
  usePageTitle('Analytics');
  const { section } = useParams();
  const { search } = useLocation();

  if (!sections.some(([id]) => id === section)) {
    return <Navigate to={`/analytics/overview${search}`} replace />;
  }

  return (
    <>
      <nav className="nav-tabs analytics-tabs" aria-label="Analytics sections">
        {sections.map(([id, label]) => (
          <NavLink key={id} className="nav-tab" to={{ pathname: `/analytics/${id}`, search }}>
            {label}
          </NavLink>
        ))}
      </nav>
      {section === 'overview' && <OverviewTab />}
      {section === 'activity' && <ActivityTab />}
      {section === 'models' && <ModelsTab />}
      {section === 'performance' && <PerformanceTab />}
      {section === 'permissions' && <PermissionsTab />}
      {section === 'logs' && <LogsTab />}
    </>
  );
}

export function LegacyAnalyticsRedirect({ section }: { section: 'overview' | 'performance' }) {
  const { search } = useLocation();
  return <Navigate to={`/analytics/${section}${search}`} replace />;
}
