// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { api } from '../../lib/api';
import type { ResolveTargetsResponse } from '../../lib/api.types';
import { ComposerMachineSelector } from './ComposerMachineSelector';
import { useShortcut, useShortcutDispatcher } from '../../lib/shortcutRegistry';

function Shortcuts({ onHistory = vi.fn() }: { onHistory?: () => void }) {
  useShortcutDispatcher();
  useShortcut({ id: 'test.history', scope: 'session', keys: { code: 'KeyH', alt: true }, description: 'Previous message', handler: onHistory });
  return <textarea aria-label="Draft prompt" />;
}

vi.mock('../../lib/api', () => ({ api: { resolveTargets: vi.fn() } }));
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
  vi.mocked(api.resolveTargets).mockResolvedValue({ candidates: [local, remote], remotes: [remote, unmatched] });
});

it('defaults to the current owner and passes the matched remote directory', async () => {
  const onSelect = vi.fn().mockResolvedValue(undefined);
  render(<ComposerMachineSelector directory="/local/repo" onSelect={onSelect} />);
  const select = await screen.findByRole('combobox', { name: 'Session machine' });
  expect(select).toHaveTextContent('This machine');
  fireEvent.click(select);
  expect(screen.getByRole('option', { name: 'Other box · no matching project' })).toBeDisabled();
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
  // The disabled option leaves the menu open.
  fireEvent.click(screen.getByRole('option', { name: 'This machine' }));
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
