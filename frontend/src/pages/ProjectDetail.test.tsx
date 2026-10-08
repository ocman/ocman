// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ProjectDetail } from './ProjectDetail';

const history = vi.hoisted(() => ({ sessions: [
  { id: 'a', title: 'Fix header', directory: '/repos/ocman' },
  { id: 'b', title: 'Add search', directory: '/repos/ocman' },
  { id: 'c', title: 'Remote change', directory: '/remote/clone', remoteId: 'other' },
  { id: 'd', title: 'Unrelated', directory: '/elsewhere' },
] }));

vi.mock('../lib/queries', () => ({
  useProjects: () => ({ isLoading: false, data: [
    { directory: '/repos/ocman', projectKey: 'git:shared' },
    { directory: '/remote/clone', remoteId: 'other', projectKey: 'git:shared' },
  ] }),
  useSessions: (params?: { limit?: number }) => ({
    isLoading: false,
    data: params?.limit === 0 ? history.sessions : history.sessions.slice(0, params?.limit ?? 500),
  }),
}));

beforeEach(() => { history.sessions = history.sessions.filter(s => !s.id.startsWith('unrelated-')); });
vi.mock('../lib/useCapabilities', () => ({ useOpencodeLaunch: () => true }));
vi.mock('../components/SessionTable', () => ({
  SessionTable: ({ sessions }: { sessions: { id: string; title: string }[] }) => (
    <ul>{sessions.map((s) => <li key={s.id}>{s.title}</li>)}</ul>
  ),
}));

it('filters project sessions by search and uses shared view tabs', () => {
  render(
    <MemoryRouter initialEntries={['/project/%2Frepos%2Focman']}>
      <div id="header-actions-slot" data-testid="header-slot" />
      <Routes><Route path="/project/:dir" element={<ProjectDetail />} /></Routes>
    </MemoryRouter>,
  );
  expect(screen.getByRole('tab', { name: 'Sessions' })).toHaveAttribute('aria-selected', 'true');
  expect(screen.queryByRole('button', { name: 'VS Code' })).not.toBeInTheDocument();
  expect(screen.getAllByRole('listitem').map(li => li.textContent)).toEqual(['Fix header', 'Add search', 'Remote change']);
  fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: 'search' } });
  expect(screen.getAllByRole('listitem').map((li) => li.textContent)).toEqual(['Add search']);
});

it('restores search from the URL', () => {
  render(<MemoryRouter initialEntries={['/project/%2Frepos%2Focman?q=search&t=0&a=1']}>
    <Routes><Route path="/project/:dir" element={<ProjectDetail />} /></Routes>
  </MemoryRouter>);
  expect(screen.getByRole('searchbox', { name: 'Search sessions' })).toHaveValue('search');
  expect(screen.getAllByRole('listitem').map((li) => li.textContent)).toEqual(['Add search']);
  expect(screen.getByRole('button', { name: 'Exclude archived' })).toHaveAttribute('aria-pressed', 'true');
});

it('keeps project sessions beyond 500 newer unrelated sessions in both time-range and All views', () => {
  history.sessions.unshift(...Array.from({ length: 500 }, (_, i) => ({
    id: `unrelated-${i}`, title: `Unrelated ${i}`, directory: '/elsewhere',
  })));
  render(
    <MemoryRouter initialEntries={['/project/%2Frepos%2Focman']}>
      <Routes><Route path="/project/:dir" element={<ProjectDetail />} /></Routes>
    </MemoryRouter>,
  );
  const expected = ['Fix header', 'Add search', 'Remote change'];
  expect(screen.getAllByRole('listitem').map(li => li.textContent)).toEqual(expected);
  fireEvent.click(screen.getByRole('radio', { name: 'All' }));
  expect(screen.getAllByRole('listitem').map(li => li.textContent)).toEqual(expected);
});
