// @vitest-environment jsdom
import { fireEvent, screen, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { expect, it, vi } from 'vitest';
import { renderWithQuery as render } from '../test/renderWithQuery';
import type { Session } from '../lib/api';
import { sortBy } from '../lib/useUrlSort';
import { SessionTable } from './SessionTable';

vi.mock('../lib/useIsPhone', () => ({ useIsPhone: () => false }));

const session = (id: string, over: Partial<Session> = {}) => ({
  id, platform: 'opencode', title: `Title ${id}`, directory: '/src/foo', status: 'done', seen: true,
  messageCount: 1, durationMs: 1000, totalCost: 0, timeCreated: 1000, timeUpdated: 1000, ...over,
} as Session);

function Search() { return <output aria-label="search">{useLocation().search}</output>; }
const titles = () => [...document.querySelectorAll('.session-title')].map(el => el.textContent?.replace('Title ', ''));

it('sorts by a numeric column from the header, largest first, keeping children under parents', () => {
  render(<MemoryRouter initialEntries={['/sessions?q=x']}>
    <SessionTable showProject={false} includeArchived sessions={[
      session('a', { totalCost: 1 }), session('b', { totalCost: 5 }), session('c', { totalCost: 3, parentId: 'a' }),
      session('d', { totalCost: 2, messageCount: 9 }),
    ]} />
    <Search />
  </MemoryRouter>);
  expect(titles()).toEqual(['a', 'c', 'b', 'd']);
  const cost = screen.getByRole('columnheader', { name: /Cost/ });
  expect(cost).toHaveAttribute('aria-sort', 'none');
  fireEvent.click(within(cost).getByRole('button'));
  expect(cost).toHaveAttribute('aria-sort', 'descending');
  expect(screen.getByLabelText('search')).toHaveTextContent('?q=x&sort=-cost');
  expect(titles()).toEqual(['b', 'd', 'a', 'c']);
  fireEvent.click(within(cost).getByRole('button'));
  expect(cost).toHaveAttribute('aria-sort', 'ascending');
  expect(titles()).toEqual(['a', 'c', 'd', 'b']);
  fireEvent.click(within(cost).getByRole('button'));
  expect(cost).toHaveAttribute('aria-sort', 'none');
  expect(screen.getByLabelText('search')).toHaveTextContent('?q=x');
});

it('restores the sort from the URL and ignores unknown keys', () => {
  const { unmount } = render(<MemoryRouter initialEntries={['/sessions?sort=-prompts']}>
    <SessionTable showProject={false} includeArchived sessions={[session('a'), session('b', { messageCount: 4 })]} />
  </MemoryRouter>);
  expect(screen.getByRole('columnheader', { name: /Prompts/ })).toHaveAttribute('aria-sort', 'descending');
  expect(titles()).toEqual(['b', 'a']);
  unmount();
  render(<MemoryRouter initialEntries={['/sessions?sort=-bogus']}>
    <SessionTable showProject={false} includeArchived sessions={[session('a'), session('b', { messageCount: 4 })]} />
  </MemoryRouter>);
  expect(titles()).toEqual(['a', 'b']);
});

it('shows one pending cell across the numeric columns while totals load', () => {
  render(<MemoryRouter><SessionTable showProject includeArchived sessions={[session('a')]} activityPendingKeys={new Set(['opencode:a'])} /></MemoryRouter>);
  expect(screen.getByText('Loading activity…')).toHaveAttribute('colspan', '3');
});

it('keeps ties in input order', () => {
  expect(sortBy([{ v: 1, n: 'x' }, { v: 2, n: 'y' }, { v: 1, n: 'z' }], { key: 'v', desc: true }, r => r.v).map(r => r.n)).toEqual(['y', 'x', 'z']);
});
