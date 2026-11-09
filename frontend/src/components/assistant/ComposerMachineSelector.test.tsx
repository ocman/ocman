// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { api, postJSON } from '../../lib/api';
import type { ResolveTargetsResponse } from '../../lib/api.types';
import { ComposerMachineSelector } from './ComposerMachineSelector';
import { useShortcut, useShortcutDispatcher } from '../../lib/shortcutRegistry';

function Shortcuts({ onHistory = vi.fn() }: { onHistory?: () => void }) {
  useShortcutDispatcher();
  useShortcut({ id: 'session.prev-user-message', scope: 'session', description: 'Previous message', handler: onHistory });
  return <textarea aria-label="Draft prompt" />;
}

vi.mock('../../lib/api', () => ({ api: { resolveTargets: vi.fn() }, postJSON: vi.fn() }));
vi.mock('../../lib/useCapabilities', () => ({ useMultiHost: () => false }));
const local = { remoteId: 'local', remoteName: 'This machine', platform: 'opencode', dir: '/local/repo' };
const remote = { remoteId: 'box', remoteName: 'Build box', platform: 'r-box:opencode', dir: '/remote/checkout' };
const unmatched = { remoteId: 'other', remoteName: 'Other box', platform: 'r-other:opencode', dir: '' };
function pick(combobox: HTMLElement, name: string | RegExp) {
  fireEvent.click(combobox);
  fireEvent.click(screen.getByRole('option', { name }));
}
beforeEach(() => {
  vi.mocked(api.resolveTargets).mockReset();
  vi.mocked(postJSON).mockReset();
  vi.mocked(api.resolveTargets).mockResolvedValue({ candidates: [local, remote], remotes: [remote, unmatched] });
});

it('defaults to the current owner and passes the matched remote directory', async () => {
  const onSelect = vi.fn().mockResolvedValue(undefined);
  render(<ComposerMachineSelector directory="/local/repo" onSelect={onSelect} />);
  const select = await screen.findByRole('combobox', { name: 'Session machine' });
  expect(select).toHaveTextContent('This machine');
  fireEvent.click(select);
  expect(screen.getByRole('option', { name: 'Other box · no matching project' })).not.toBeDisabled();
  fireEvent.click(screen.getByRole('option', { name: 'Build box' }));
  await waitFor(() => expect(onSelect).toHaveBeenCalledWith(remote));
  expect(api.resolveTargets).toHaveBeenCalledWith('/local/repo', 'local');
});

it('opens the host picker with Option+H while typing instead of navigating history', async () => {
  const onSelect = vi.fn().mockResolvedValue(undefined);
  const onHistory = vi.fn();
  render(<><Shortcuts onHistory={onHistory} /><ComposerMachineSelector directory="/local/repo" onSelect={onSelect} /></>);
  await screen.findByRole('combobox');
  const input = screen.getByRole('textbox', { name: 'Draft prompt' });
  input.focus();
  expect(fireEvent.keyDown(input, { code: 'KeyH', key: '˙', altKey: true })).toBe(false);
  expect(screen.getByRole('textbox', { name: 'Search machines' })).toHaveFocus();
  expect(onHistory).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('option', { name: 'Build box' }));
  await waitFor(() => expect(onSelect).toHaveBeenCalledWith(remote));
});

it('does not open the host picker while controls are locked', async () => {
  const { rerender } = render(<><Shortcuts /><ComposerMachineSelector directory="/local/repo" disabled onSelect={vi.fn()} /></>);
  await screen.findByRole('combobox');
  fireEvent.keyDown(window, { code: 'KeyH', altKey: true });
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  rerender(<><Shortcuts /><ComposerMachineSelector directory="/local/repo" onSelect={vi.fn()} /></>);
  fireEvent.keyDown(window, { code: 'KeyH', altKey: true });
  expect(screen.getByRole('listbox')).toBeInTheDocument();
});

it('supports remote-to-local selection without assuming matching paths', async () => {
  const onSelect = vi.fn().mockResolvedValue(undefined);
  render(<ComposerMachineSelector directory="/remote/checkout" remoteId="box" onSelect={onSelect} />);
  const select = await screen.findByRole('combobox');
  expect(select).toHaveTextContent('Build box');
  expect(select.querySelector('.host-badge')).toHaveAttribute('aria-label', 'Build box');
  expect(select.querySelector('.bi-hdd-network')).toHaveAttribute('aria-hidden', 'true');
  pick(select, 'This machine');
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
  pick(select, /Other box/);
  expect(screen.getByRole('dialog')).toHaveTextContent('Local uncommitted changes and unpushed commits will not transfer');
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  pick(select, 'This machine');
  expect(onSelect).not.toHaveBeenCalled();
});

it('checks a destination and confirms cloning on submit without starting a session', async () => {
  vi.mocked(api.resolveTargets).mockResolvedValue({ candidates: [local], remotes: [unmatched], cloneUrl: 'https://example.com/Org/Repo.git' });
  vi.mocked(postJSON).mockResolvedValue({ url: 'https://example.com/Org/Repo.git', directory: '/owner/src/repo' });
  const onSelect = vi.fn().mockResolvedValue(undefined);
  render(<ComposerMachineSelector directory="/local/repo" onSelect={onSelect} />);
  pick(await screen.findByRole('combobox'), /Other box/);
  await waitFor(() => expect(screen.getByLabelText('Destination directory')).toHaveValue('/owner/src/repo'));
  expect(onSelect).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText('Destination directory'), { target: { value: '/chosen/repo' } });
  vi.mocked(postJSON).mockResolvedValueOnce({ url: 'https://example.com/Org/Repo.git', directory: '/chosen/repo' });
  fireEvent.click(screen.getByRole('button', { name: 'Use machine and clone on submit' }));
  await waitFor(() => expect(onSelect).toHaveBeenCalledWith({ ...unmatched, dir: '/chosen/repo', cloneUrl: 'https://example.com/Org/Repo.git' }));
  expect(postJSON).toHaveBeenLastCalledWith('/api/sessions/plan-clone', { remoteId: 'other', url: 'https://example.com/Org/Repo.git', directory: '/chosen/repo' });
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
});

it('keeps planning failures retryable and cancellation does not select a machine', async () => {
  vi.mocked(api.resolveTargets).mockResolvedValue({ candidates: [local], remotes: [unmatched], cloneUrl: 'https://example.com/repo' });
  vi.mocked(postJSON).mockRejectedValueOnce(new Error('Destination already exists'));
  const onSelect = vi.fn();
  render(<ComposerMachineSelector directory="/local/repo" onSelect={onSelect} />);
  pick(await screen.findByRole('combobox'), /Other box/);
  expect(await screen.findByRole('alert')).toHaveTextContent('Destination already exists');
  expect(screen.getByRole('button', { name: 'Use machine and clone on submit' })).not.toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(onSelect).not.toHaveBeenCalled();
});

it('resolves choices from the original project while a clone is pending', async () => {
  render(<ComposerMachineSelector directory="/new/destination" remoteId="other" source={{ directory: '/local/repo', remoteId: 'local' }} onSelect={vi.fn()} />);
  await screen.findByRole('combobox');
  expect(api.resolveTargets).toHaveBeenCalledWith('/local/repo', 'local');
});

it('reports resolution and launch failures, and can retry on focus', async () => {
  vi.mocked(api.resolveTargets).mockRejectedValueOnce(new Error('offline'));
  const onSelect = vi.fn().mockRejectedValue(new Error('launch failed'));
  render(<ComposerMachineSelector directory="/local/repo" onSelect={onSelect} />);
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load machines');
  expect(screen.getByRole('combobox')).toBeDisabled();
  fireEvent.focus(window);
  await waitFor(() => expect(screen.getByRole('combobox')).not.toBeDisabled());
  pick(screen.getByRole('combobox'), 'Build box');
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
  expect(select).toHaveTextContent('Current machine');
  expect(select).toBeDisabled();
});
