// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { NotFound } from './NotFound';
import { suggestDestination } from '../lib/suggestDestination';
import { useBetaFeatures } from '../lib/betaFeatures';

const destinations = [
  { to: '/sessions', label: 'Sessions' },
  { to: '/routines', label: 'Routines' },
  { to: '/provider-usage', label: 'Provider usage' },
  { to: '/sessions?activity=resources', label: 'Activity' },
];

describe('suggestDestination', () => {
  it.each([
    ['/routine', 'Routines'],
    ['/Routines/abc', 'Routines'],
    ['/activity', 'Activity'],
    ['/provider%20usage', 'Provider usage'],
    ['/session', 'Sessions'],
  ])('suggests a destination for %s', (path, label) => {
    expect(suggestDestination(path, destinations)?.label).toBe(label);
  });

  it.each(['/', '/xy', '/does-not-exist', '/%FF', '/routines%E0%A4%A'])('suggests nothing for %s', (path) => {
    expect(suggestDestination(path, destinations)).toBeUndefined();
  });
});

describe('NotFound', () => {
  beforeEach(() => useBetaFeatures.setState({ factory: false }));

  function renderAt(path: string) {
    render(<MemoryRouter initialEntries={[path]}><NotFound /></MemoryRouter>);
  }

  it('stays usable on a path with malformed percent-encoding', () => {
    renderAt('/%FF');
    expect(screen.getByRole('heading', { name: 'Page not found' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Go to Home' })).toHaveAttribute('href', '/');
  });

  it('opens the Activity panel for a guessed /activity route', () => {
    renderAt('/activity');
    expect(screen.getByText('Activity', { selector: 'strong' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Open Activity' })).toHaveAttribute('href', '/sessions?activity=resources');
  });

  it('lists other pages without hidden beta features when nothing matches', () => {
    renderAt('/does-not-exist');
    expect(screen.queryByRole('link', { name: /^Open / })).not.toBeInTheDocument();
    const others = screen.getByRole('navigation', { name: 'Other pages' });
    expect(others).toHaveTextContent('Routines');
    expect(others).not.toHaveTextContent('Factory');
  });
});
