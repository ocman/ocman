// @vitest-environment jsdom

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import { RightPanel } from './RightPanel';
import { useUiStore } from '../lib/uiStore';
import type { BeadsStatus } from '../lib/beadsApi';
import type { Session } from '../lib/api';

let beadsResult: {
  data: BeadsStatus | undefined;
  error: Error | null;
  isFetching: boolean;
  refetch: ReturnType<typeof vi.fn>;
};
let beadsArgs: Parameters<typeof import('../lib/useBeadsStatus').useBeadsStatus>;

vi.mock('../lib/useBeadsStatus', () => ({
  useBeadsStatus: (...args: typeof beadsArgs) => {
    beadsArgs = args;
    return beadsResult;
  },
}));
vi.mock('../lib/useUpstreams', () => ({
  useUpstreams: () => ({ upstreams: [] }),
}));

const props = {
  sessionId: 's1',
  platformId: 'opencode',
  directory: '/repo',
  session: { remoteId: '' } as Session,
  messageBookmarkGroups: [],
  selectedMessageBookmarkKey: null,
  onRemoveMessageBookmark: vi.fn(),
  onScrollToMessageBookmark: vi.fn(),
};

beforeEach(() => {
  useUiStore.persist.setOptions({
    storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
  });
  beadsResult = { data: undefined, error: null, isFetching: false, refetch: vi.fn() };
  useUiStore.setState({
    changesSidebarOpenTabs: ['beads'],
    changesSidebarTabOrder: ['info', 'session', 'working-tree', 'bookmarks', 'upstream', 'beads'],
    changesSidebarTabSizes: {},
  });
});

it('does not expose unresolved or unavailable Beads persisted state', () => {
  const { rerender } = render(<RightPanel {...props} />);
  expect(screen.queryByRole('tab', { name: 'Beads' })).not.toBeInTheDocument();
  expect(screen.getByLabelText('Changes (collapsed)')).toBeInTheDocument();

  beadsResult = { ...beadsResult, data: { available: false } };
  rerender(<RightPanel {...props} />);
  expect(screen.queryByRole('tab', { name: 'Beads' })).not.toBeInTheDocument();
});

it('restores the persisted Beads pane and refreshes when available', async () => {
  beadsResult = {
    data: { available: true, tickets: [{ id: 'bd-1', title: 'Ticket', status: 'open', priority: 1 }] },
    error: null,
    isFetching: false,
    refetch: vi.fn(),
  };
  render(<RightPanel {...props} />);

  expect(screen.getByRole('tab', { name: 'Beads' })).toHaveAttribute('aria-selected', 'true');
  expect(screen.getByText('bd-1')).toBeInTheDocument();
  await userEvent.click(await screen.findByRole('button', { name: 'Refresh' }));
  expect(beadsResult.refetch).toHaveBeenCalledOnce();
});

it('passes the remote owner to the resource', () => {
  render(<RightPanel {...props} session={{ remoteId: 'abc' } as Session} />);

  expect(beadsArgs).toEqual(['/repo', 'abc', true]);
  expect(screen.queryByRole('tab', { name: 'Beads' })).not.toBeInTheDocument();
});

it('hides an open Beads pane while availability resolves for a new repository', () => {
  beadsResult = {
    data: { available: true, tickets: [] },
    error: null,
    isFetching: false,
    refetch: vi.fn(),
  };
  const { rerender } = render(<RightPanel {...props} />);
  expect(screen.getByRole('tab', { name: 'Beads' })).toBeInTheDocument();

  beadsResult = { ...beadsResult, data: undefined };
  rerender(<RightPanel {...props} directory="/other" />);
  expect(screen.queryByRole('tab', { name: 'Beads' })).not.toBeInTheDocument();
  expect(screen.getByLabelText('Changes (collapsed)')).toBeInTheDocument();

  beadsResult = { ...beadsResult, data: { available: true, tickets: [] } };
  rerender(<RightPanel {...props} directory="/other" />);
  expect(screen.getByRole('tab', { name: 'Beads' })).toHaveAttribute('aria-selected', 'true');
});

it('keeps a discovered Beads pane visible with stale tickets after a refresh error', () => {
  beadsResult = {
    data: {
      available: true,
      error: 'status_unavailable',
      tickets: [{ id: 'bd-stale', title: 'Stale ticket', status: 'open', priority: 1 }],
    },
    error: null,
    isFetching: false,
    refetch: vi.fn(),
  };

  render(<RightPanel {...props} />);

  expect(screen.getByRole('tab', { name: 'Beads' })).toBeInTheDocument();
  expect(screen.getByText('bd-stale')).toBeInTheDocument();
  expect(screen.getByRole('alert')).toHaveTextContent('Could not refresh Beads tickets');
});
