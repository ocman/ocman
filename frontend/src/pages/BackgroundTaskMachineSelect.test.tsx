// @vitest-environment jsdom
import { expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { useRemotes } from '../lib/settingsQueries';
import { BackgroundTaskMachineSelect } from './BackgroundTaskMachineSelect';

vi.mock('../lib/settingsQueries', () => ({ useRemotes: vi.fn() }));

function remotes(overrides: Record<string, unknown> = {}) {
  vi.mocked(useRemotes).mockReturnValue({ data: [], isError: false, isFetching: false, ...overrides } as unknown as ReturnType<typeof useRemotes>);
}

it('hides the chooser on single-machine instances', () => {
  remotes();
  render(<BackgroundTaskMachineSelect value="local" onChange={vi.fn()} />);
  expect(screen.queryByRole('button')).not.toBeInTheDocument();
});

it('deduplicates known owners, skips unidentified remotes, and falls back to hostname or ID', () => {
  remotes({ data: [
    { remoteId: 'one', displayName: 'First machine', enabled: true, health: 'connected' },
    { remoteId: 'one', displayName: 'Duplicate' }, { displayName: 'Unidentified' },
    { remoteId: 'two', hostname: 'second-host', enabled: false, health: 'connected' },
    { remoteId: 'three', enabled: true, health: 'disconnected' },
  ] });
  const change = vi.fn();
  render(<BackgroundTaskMachineSelect value="one" onChange={change} />);
  const toggle = screen.getByRole('button', { name: 'Task machine: First machine' });
  fireEvent.click(toggle);
  const select = screen.getByRole('combobox', { name: 'Task machine' });
  expect(select).toHaveFocus();
  expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual([
    'This machine', 'First machine', 'second-host (offline)', 'three (offline)',
  ]);
  fireEvent.change(select, { target: { value: 'local' } });
  expect(change).toHaveBeenCalledWith('local');
  expect(toggle).toHaveFocus();
  expect(toggle).toHaveAttribute('aria-expanded', 'false');
  fireEvent.click(toggle);
  fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Escape' });
  expect(toggle).toHaveFocus();
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  fireEvent.click(toggle);
  fireEvent.click(toggle);
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
});

it('offers retry when remote discovery fails instead of silently hiding remote access', () => {
  const refetch = vi.fn();
  remotes({ data: undefined, isError: true, refetch });
  render(<BackgroundTaskMachineSelect value="missing" onChange={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Task machine: missing' }));
  expect(screen.getByRole('alert')).toHaveTextContent('Could not load machines.');
  expect(screen.getByRole('option', { name: 'missing (offline)' })).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  expect(refetch).toHaveBeenCalledOnce();
});
