import { Navigate, NavLink, useLocation, useParams } from 'react-router-dom';
import { usePageTitle } from '../../lib/headerContext';
import { StatsTab } from './StatsTab';
import { UsageTab } from './UsageTab';

const sections = [
  ['overview', 'Overview'],
  ['performance', 'Performance'],
  ['logs', 'Logs'],
] as const;

export function AnalyticsTab() {
  usePageTitle('Analytics');
  const { section } = useParams();
  const { search } = useLocation();

  if (section !== 'overview' && section !== 'performance' && section !== 'logs') {
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
      {section === 'overview'
        ? <UsageTab />
        : <StatsTab view={section} />}
    </>
  );
}

export function LegacyAnalyticsRedirect({ section }: { section: 'overview' | 'performance' }) {
  const { search } = useLocation();
  return <Navigate to={`/analytics/${section}${search}`} replace />;
}
