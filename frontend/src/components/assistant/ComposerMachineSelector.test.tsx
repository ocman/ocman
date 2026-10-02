// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { api } from '../../lib/api';
import type { ResolveTargetsResponse } from '../../lib/api.types';
import { ComposerMachineSelector } from './ComposerMachineSelector';

vi.mock('../../lib/api', () => ({ api: { resolveTargets: vi.fn() } }));
const local = { remoteId: 'local', remoteName: 'This machine', platform: 'opencode', dir: '/local/repo' };
const remote = { remoteId: 'box', remoteName: 'Build box', platform: 'r-box:opencode', dir: '/remote/checkout' };
const unmatched = { remoteId: 'other', remoteName: 'Other box', platform: 'r-other:opencode', dir: '' };
beforeEach(() => {
  vi.mocked(api.resolveTargets).mockReset();
  vi.mocked(api.resolveTargets).mockResolvedValue({ candidates: [local, remote], remotes: [remote, unmatched] });
});

it('defaults to the current owner and passes the matched remote directory', async () => {
  const onSelect = vi.fn().mockResolvedValue(undefined);
  render(<ComposerMachineSelector directory="/local/repo" onSelect={onSelect} />);
  const select = await screen.findByRole('combobox', { name: 'Session machine' });
  expect(select).toHaveValue('local');
  expect(screen.getByRole('option', { name: 'Other box · no matching project' })).toBeDisabled();
  fireEvent.change(select, { target: { value: 'box' } });
  await waitFor(() => expect(onSelect).toHaveBeenCalledWith(remote));
  expect(api.resolveTargets).toHaveBeenCalledWith('/local/repo', 'local');
});

it('supports remote-to-local selection without assuming matching paths', async () => {
  const onSelect = vi.fn().mockResolvedValue(undefined);
  render(<ComposerMachineSelector directory="/remote/checkout" remoteId="box" onSelect={onSelect} />);
  const select = await screen.findByRole('combobox');
  expect(select).toHaveValue('box');
  fireEvent.change(select, { target: { value: 'local' } });
  await waitFor(() => expect(onSelect).toHaveBeenCalledWith(local));
  expect(api.resolveTargets).toHaveBeenCalledWith('/remote/checkout', 'box');
});

it('is hidden on a single-host installation', async () => {
  vi.mocked(api.resolveTargets).mockResolvedValue({ candidates: [local], remotes: [] });
  const { container } = render(<ComposerMachineSelector directory="/local/repo" onSelect={vi.fn()} />);
  await act(async () => {});
  expect(container).toBeEmptyDOMElement();
});

it('shows unmatched machines without launching and ignores the current choice', async () => {
  const onSelect = vi.fn();
  vi.mocked(api.resolveTargets).mockResolvedValue({ candidates: [], remotes: [unmatched] });
  render(<ComposerMachineSelector directory="/local/repo" onSelect={onSelect} />);
  const select = await screen.findByRole('combobox');
  fireEvent.change(select, { target: { value: 'other' } });
  fireEvent.change(select, { target: { value: 'local' } });
  expect(onSelect).not.toHaveBeenCalled();
});

it('reports resolution and launch failures, and can retry on focus', async () => {
  vi.mocked(api.resolveTargets).mockRejectedValueOnce(new Error('offline'));
  const onSelect = vi.fn().mockRejectedValue(new Error('launch failed'));
  render(<ComposerMachineSelector directory="/local/repo" onSelect={onSelect} />);
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load machines');
  expect(screen.getByRole('combobox')).toBeDisabled();
  fireEvent.focus(window);
  await waitFor(() => expect(screen.getByRole('combobox')).not.toBeDisabled());
  fireEvent.change(screen.getByRole('combobox'), { target: { value: 'box' } });
  expect(await screen.findByText('Could not start a session on that machine')).toBeInTheDocument();
});

it('ignores a response arriving after unmount', async () => {
  let resolve!: (response: ResolveTargetsResponse) => void;
  vi.mocked(api.resolveTargets).mockReturnValueOnce(new Promise((r) => { resolve = r; }));
  const { unmount } = render(<ComposerMachineSelector directory="/local/repo" onSelect={vi.fn()} />);
  unmount();
  await act(async () => resolve({ candidates: [local], remotes: [] }));
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
});

it('keeps a disconnected current owner visible and respects the busy lock', async () => {
  render(<ComposerMachineSelector directory="/remote/repo" remoteId="gone" disabled onSelect={vi.fn()} />);
  const select = await screen.findByRole('combobox');
  expect(select).toHaveValue('gone');
  expect(select).toBeDisabled();
});
