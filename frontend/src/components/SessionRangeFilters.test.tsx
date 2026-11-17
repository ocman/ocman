// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { expect, it } from 'vitest';
import { useSessionListUrlFilters } from '../lib/useSessionListUrlFilters';
import { useUiStore } from '../lib/uiStore';
import { SessionRangeFilters } from './SessionRangeFilters';
import { timeRangeLabel } from '../lib/format';

function Harness() {
  const f = useSessionListUrlFilters();
  return <>
    <SessionRangeFilters label="Filters" timeRange={f.timeRange} onTimeRangeChange={f.setTimeRange}
      showArchived={f.showArchived} onShowArchivedChange={f.setShowArchived} />
    <output aria-label="since">{String(f.sinceHours)}</output>
    <output aria-label="search">{useLocation().search}</output>
  </>;
}
const mount = (url: string) => render(<MemoryRouter initialEntries={[url]}><Harness /></MemoryRouter>);

it('defaults to the configured window, shows it as a choice and hides archived', () => {
  useUiStore.setState({ dashboardTimeRangeDefault: 72 });
  mount('/sessions');
  expect(screen.getAllByRole('radio').map(r => (r as HTMLInputElement).labels?.[0]?.textContent)).toEqual(['12h', '24h', '3d', '7d', '30d', 'All']);
  expect(screen.getByRole('radio', { name: '3d' })).toBeChecked();
  expect(screen.getByRole('button', { name: 'Show archived' })).toHaveAttribute('aria-pressed', 'false');
  expect(screen.getByLabelText('since')).toHaveTextContent('72');
});

it('stores choices in the URL and treats All as unbounded', () => {
  mount('/sessions?t=24&q=x');
  fireEvent.click(screen.getByRole('radio', { name: 'All' }));
  fireEvent.click(screen.getByRole('button', { name: 'Show archived' }));
  expect(screen.getByLabelText('since')).toHaveTextContent('undefined');
  expect(screen.getByLabelText('search')).toHaveTextContent('?t=0&q=x&a=1');
  expect(screen.getByRole('button', { name: 'Show archived' })).toHaveAttribute('aria-pressed', 'true');
  fireEvent.click(screen.getByRole('button', { name: 'Show archived' }));
  expect(screen.getByLabelText('search')).toHaveTextContent('?t=0&q=x');
});

it('ignores malformed ranges and labels custom windows', () => {
  useUiStore.setState({ dashboardTimeRangeDefault: 36 });
  mount('/sessions?t=abc');
  expect(screen.getByRole('radio', { name: '36h' })).toBeChecked();
  expect(timeRangeLabel(48)).toBe('2d');
});
