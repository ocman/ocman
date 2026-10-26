// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { PermissionModeLock } from './PermissionModeLock';

const mocks = vi.hoisted(() => ({
  get: vi.fn(), setRules: vi.fn(), setNative: vi.fn(), changed: undefined as undefined | ((id: string) => void),
}));
vi.mock('../lib/api', () => ({ api: {
  getPermissionRules: mocks.get, setPermissionRules: mocks.setRules, setNativePermissionMode: mocks.setNative,
} }));
vi.mock('../lib/useGlobalEvents', () => ({ onSessionChanged: (cb: (id: string) => void) => { mocks.changed = cb; return () => {}; } }));

beforeEach(() => {
  vi.clearAllMocks();
  mocks.changed = undefined;
  Element.prototype.scrollIntoView = vi.fn();
});

function nativeAutoIndicator() {
  return screen.getByRole('option', { name: /Native Auto/i }).querySelector('[data-active]');
}

it('does not offer native Auto when the owner does not advertise it', async () => {
  mocks.get.mockResolvedValue({ rules: [] });
  render(<PermissionModeLock sessionId="s" />);
  await userEvent.click(await screen.findByLabelText('Permission mode: Default'));
  expect(screen.queryByRole('option', { name: /Native Auto/i })).not.toBeInTheDocument();
  expect(screen.queryByText('Platform modes')).not.toBeInTheDocument();
});

it('changes the native engine without replacing ocman restrictions', async () => {
  const rules = [{ permission: 'edit', pattern: '*', action: 'deny' }];
  mocks.get.mockResolvedValueOnce({ rules, nativeMode: { mode: 'default' } }).mockResolvedValue({ rules, nativeMode: { mode: 'auto' } });
  mocks.setNative.mockResolvedValue(undefined);
  render(<PermissionModeLock sessionId="s" />);
  await userEvent.click(await screen.findByLabelText('Permission mode: Custom'));
  expect(screen.getByText('Ocman modes')).toBeInTheDocument();
  expect(screen.getByText('Platform modes')).toBeInTheDocument();
  expect(nativeAutoIndicator()).toHaveAttribute('data-active', 'false');
  await userEvent.click(screen.getByRole('option', { name: /Native Auto/i }));
  await waitFor(() => expect(mocks.setNative).toHaveBeenCalledWith('s', 'auto'));
  expect(await screen.findByLabelText('Permission mode: Custom + native Auto')).toBeInTheDocument();
  await userEvent.click(screen.getByLabelText('Permission mode: Custom + native Auto'));
  expect(nativeAutoIndicator()).toHaveAttribute('data-active', 'true');
  expect(mocks.setRules).not.toHaveBeenCalled();
});

it('can disable native Auto independently of ocman rules', async () => {
  mocks.get.mockResolvedValueOnce({ rules: [], nativeMode: { mode: 'auto' } }).mockResolvedValue({ rules: [], nativeMode: { mode: 'default' } });
  mocks.setNative.mockResolvedValue(undefined);
  render(<PermissionModeLock sessionId="s" />);
  await userEvent.click(await screen.findByLabelText('Permission mode: Default + native Auto'));
  expect(nativeAutoIndicator()).toHaveAttribute('data-active', 'true');
  expect(screen.getByRole('option', { name: /^Default/ }).querySelector('[data-active]')).toHaveAttribute('data-active', 'true');
  await userEvent.click(screen.getByRole('option', { name: /Native Auto/i }));
  expect(await screen.findByLabelText('Permission mode: Default')).toBeInTheDocument();
  expect(mocks.setNative).toHaveBeenCalledWith('s', 'default');
  expect(mocks.setRules).not.toHaveBeenCalled();
  await userEvent.click(screen.getByLabelText('Permission mode: Default'));
  expect(nativeAutoIndicator()).toHaveAttribute('data-active', 'false');
});

it('keeps the effective mode when the CLI rejects an unsupported model', async () => {
  mocks.get.mockResolvedValue({ rules: [], nativeMode: { mode: 'default' } });
  mocks.setNative.mockRejectedValue(new Error('auto mode unavailable for this model'));
  render(<PermissionModeLock sessionId="s" />);
  await userEvent.click(await screen.findByLabelText('Permission mode: Default'));
  await userEvent.click(screen.getByRole('option', { name: /Native Auto/i }));
  expect(await screen.findByText('auto mode unavailable for this model')).toBeInTheDocument();
  expect(screen.getByLabelText('Permission mode: Default')).toBeInTheDocument();
  await userEvent.click(screen.getByLabelText('Permission mode: Default'));
  expect(nativeAutoIndicator()).toHaveAttribute('data-active', 'false');
});

it('refreshes the effective mode after a session change broadcast', async () => {
  mocks.get.mockResolvedValueOnce({ rules: [], nativeMode: { mode: 'auto' } }).mockResolvedValue({ rules: [], nativeMode: { mode: 'default' } });
  render(<PermissionModeLock sessionId="s" />);
  await screen.findByLabelText('Permission mode: Default + native Auto');
  mocks.changed?.('other');
  expect(mocks.get).toHaveBeenCalledTimes(1);
  mocks.changed?.('s');
  expect(await screen.findByLabelText('Permission mode: Default')).toBeInTheDocument();
});

it('marks saved Auto as pending until a child accepts it', async () => {
  mocks.get.mockResolvedValue({ rules: [], nativeMode: { mode: 'auto', pending: true } });
  render(<PermissionModeLock sessionId="s" />);
  expect(await screen.findByLabelText('Permission mode: Default + native Auto (next turn)')).toBeInTheDocument();
  await userEvent.click(screen.getByLabelText('Permission mode: Default + native Auto (next turn)'));
  expect(nativeAutoIndicator()).toHaveAttribute('data-active', 'true');
  expect(screen.getByText(/Applies on the next turn/)).toBeInTheDocument();
});

it('toggles native Auto through the picker keyboard controls', async () => {
  mocks.get.mockResolvedValueOnce({ rules: [], nativeMode: { mode: 'default' } }).mockResolvedValue({ rules: [], nativeMode: { mode: 'auto' } });
  mocks.setNative.mockResolvedValue(undefined);
  render(<PermissionModeLock sessionId="s" />);
  await userEvent.click(await screen.findByLabelText('Permission mode: Default'));
  await userEvent.type(screen.getByRole('combobox'), 'native Auto');
  expect(nativeAutoIndicator()).toHaveAttribute('data-active', 'false');
  await userEvent.keyboard('{Enter}');
  expect(await screen.findByLabelText('Permission mode: Default + native Auto')).toBeInTheDocument();
  expect(mocks.setNative).toHaveBeenCalledExactlyOnceWith('s', 'auto');
});

it('skips section headers when navigating from ocman modes to platform modes', async () => {
  mocks.get.mockResolvedValueOnce({ rules: [], nativeMode: { mode: 'default' } }).mockResolvedValue({ rules: [], nativeMode: { mode: 'auto' } });
  mocks.setNative.mockResolvedValue(undefined);
  render(<PermissionModeLock sessionId="s" />);
  await userEvent.click(await screen.findByLabelText('Permission mode: Default'));
  expect(screen.getByText('Ocman modes')).toBeInTheDocument();
  expect(screen.getByText('Platform modes')).toBeInTheDocument();
  await userEvent.click(screen.getByRole('combobox'));
  await userEvent.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}{ArrowDown}{Enter}');
  expect(await screen.findByLabelText('Permission mode: Default + native Auto')).toBeInTheDocument();
  expect(mocks.setNative).toHaveBeenCalledExactlyOnceWith('s', 'auto');
});
