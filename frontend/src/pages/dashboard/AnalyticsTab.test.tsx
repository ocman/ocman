// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';

vi.mock('./OverviewTab', () => ({ OverviewTab: () => <div>analytics:overview</div> }));
vi.mock('./ActivityTab', () => ({ ActivityTab: () => <div>analytics:activity</div> }));
vi.mock('./ModelsTab', () => ({ ModelsTab: () => <div>analytics:models</div> }));
vi.mock('./PerformanceTab', () => ({ PerformanceTab: () => <div>analytics:performance</div> }));
vi.mock('./PermissionsTab', () => ({ PermissionsTab: () => <div>analytics:permissions</div> }));
vi.mock('./LogsTab', () => ({ LogsTab: () => <div>analytics:logs</div> }));

vi.mock('../../lib/headerContext', () => ({
  usePageTitle: vi.fn(),
}));

import { AnalyticsTab, LegacyAnalyticsRedirect } from './AnalyticsTab';

function LocationMarker() {
  const location = useLocation();
  return <div>{location.pathname}{location.search}</div>;
}

describe('AnalyticsTab', () => {
  it.each([
    ['/analytics/overview', 'analytics:overview'],
    ['/analytics/activity', 'analytics:activity'],
    ['/analytics/models', 'analytics:models'],
    ['/analytics/performance', 'analytics:performance'],
    ['/analytics/permissions', 'analytics:permissions'],
    ['/analytics/logs', 'analytics:logs'],
  ])('renders the selected section at %s', (path, content) => {
    render(
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/analytics/:section" element={<AnalyticsTab />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(screen.getByRole('navigation', { name: 'Analytics sections' })).toBeInTheDocument();
    expect(screen.getByText(content)).toBeInTheDocument();
  });

  it.each([
    ['/usage?dir=%2Frepos', 'overview'],
    ['/stats?dir=%2Frepos', 'performance'],
  ])('preserves filters when redirecting %s', (path, section) => {
    render(
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/usage" element={<LegacyAnalyticsRedirect section="overview" />} />
          <Route path="/stats" element={<LegacyAnalyticsRedirect section="performance" />} />
          <Route path="/analytics/:section" element={<LocationMarker />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(screen.getByText(`/analytics/${section}?dir=%2Frepos`)).toBeInTheDocument();
  });

  it('redirects unknown sections and keeps filters on section links', () => {
    render(
      <MemoryRouter initialEntries={['/analytics/unknown?dir=%2Frepos&t=7']}>
        <Routes>
          <Route path="/analytics/:section" element={<AnalyticsTab />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(screen.getByText('analytics:overview')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Performance' }))
      .toHaveAttribute('href', '/analytics/performance?dir=%2Frepos&t=7');
  });
});
