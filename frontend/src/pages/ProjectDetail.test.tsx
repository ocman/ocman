// @vitest-environment jsdom
import { expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ProjectDetail } from './ProjectDetail';

vi.mock('../lib/queries', () => ({
  useSessions: () => ({
    isLoading: false,
    data: [
      { id: 'a', title: 'Fix header', directory: '/repos/ocman' },
      { id: 'b', title: 'Add search', directory: '/repos/ocman' },
    ],
  }),
}));
vi.mock('../lib/useTmux', () => ({ useTmux: () => ({ findSession: () => undefined, clients: [] }) }));
vi.mock('../lib/useCapabilities', () => ({ useOpencodeLaunch: () => true }));
vi.mock('../components/SessionTable', () => ({
  SessionTable: ({ sessions }: { sessions: { id: string; title: string }[] }) => (
    <ul>{sessions.map((s) => <li key={s.id}>{s.title}</li>)}</ul>
  ),
}));

it('filters project sessions by search and portals actions into the header', () => {
  render(
    <MemoryRouter initialEntries={['/project/%2Frepos%2Focman']}>
      <div id="header-actions-slot" data-testid="header-slot" />
      <Routes><Route path="/project/:dir" element={<ProjectDetail />} /></Routes>
    </MemoryRouter>,
  );
  expect(screen.getByTestId('header-slot')).toHaveTextContent('Worktrees');
  expect(screen.getAllByRole('listitem')).toHaveLength(2);
  fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: 'search' } });
  expect(screen.getAllByRole('listitem').map((li) => li.textContent)).toEqual(['Add search']);
});
