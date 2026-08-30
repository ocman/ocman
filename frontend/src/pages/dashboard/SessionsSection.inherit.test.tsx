// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';

vi.hoisted(() => {
  const mem = new Map<string, string>();
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => mem.get(key) ?? null,
      setItem: (key: string, value: string) => void mem.set(key, value),
      removeItem: (key: string) => void mem.delete(key),
    },
  });
});

import { SessionsSection } from './SettingsSections';
import { useUiStore } from '../../lib/uiStore';

// Mock the server-backed settings so these rows load and save known values.
vi.mock('../../lib/api', () => ({
  api: {
    getWorktreeInheritPermissions: vi.fn(),
    setWorktreeInheritPermissions: vi.fn(),
    getAutoArchiveSettings: vi.fn(),
    setAutoArchiveSettings: vi.fn(),
  },
}));
import { api } from '../../lib/api';
const m = api as unknown as Record<string, ReturnType<typeof vi.fn>>;

afterEach(() => {
  vi.clearAllMocks();
  useUiStore.getState().setShowMessageMetadata(false);
});

function mockDefaults() {
  m.getWorktreeInheritPermissions.mockResolvedValue({ enabled: true });
  m.getAutoArchiveSettings.mockResolvedValue({ enabled: true, ttlDays: 7 });
}

describe('SessionsSection worktree inherit toggle (#101)', () => {
  it('reflects the loaded enabled state', async () => {
    mockDefaults();
    m.getWorktreeInheritPermissions.mockResolvedValue({ enabled: false });
    render(<SessionsSection />);
    const toggle = await screen.findByTestId('worktree-inherit-toggle');
    await waitFor(() => expect((toggle as HTMLInputElement).checked).toBe(false));
  });

  it('persists the toggle when switched off', async () => {
    mockDefaults();
    m.getWorktreeInheritPermissions.mockResolvedValue({ enabled: true });
    m.setWorktreeInheritPermissions.mockResolvedValue({ enabled: false });
    render(<SessionsSection />);
    const toggle = await screen.findByTestId('worktree-inherit-toggle');
    await waitFor(() => expect((toggle as HTMLInputElement).checked).toBe(true));

    await act(async () => {
      fireEvent.click(toggle);
    });
    await waitFor(() => expect(m.setWorktreeInheritPermissions).toHaveBeenCalledWith(false));
  });

  it('reverts the toggle when the save fails', async () => {
    mockDefaults();
    m.getWorktreeInheritPermissions.mockResolvedValue({ enabled: true });
    m.setWorktreeInheritPermissions.mockRejectedValue(new Error('boom'));
    render(<SessionsSection />);
    const toggle = await screen.findByTestId('worktree-inherit-toggle');
    await waitFor(() => expect((toggle as HTMLInputElement).checked).toBe(true));

    await act(async () => {
      fireEvent.click(toggle);
    });
    await waitFor(() => expect((toggle as HTMLInputElement).checked).toBe(true));
  });
});

describe('SessionsSection message metadata toggle', () => {
  it('shows message metadata only after the setting is enabled', async () => {
    mockDefaults();
    m.getWorktreeInheritPermissions.mockResolvedValue({ enabled: true });
    render(<SessionsSection />);

    const toggle = await screen.findByRole('checkbox', { name: 'Show metadata between message sections' });
    expect(toggle).not.toBeChecked();

    fireEvent.click(toggle);

    expect(useUiStore.getState()).toMatchObject({ showMessageMetadata: true });
  });
});

describe('SessionsSection auto-archive settings', () => {
  it('disables controls until settings load', () => {
    m.getWorktreeInheritPermissions.mockResolvedValue({ enabled: true });
    m.getAutoArchiveSettings.mockReturnValue(new Promise(() => {}));

    render(<SessionsSection />);

    expect(screen.getByRole('checkbox', { name: 'Automatically archive inactive sessions and projects' })).toBeDisabled();
    expect(screen.queryByRole('spinbutton', { name: 'Archive inactive sessions and projects after days' })).not.toBeInTheDocument();
  });

  it('loads the toggle and TTL', async () => {
    mockDefaults();
    m.getAutoArchiveSettings.mockResolvedValue({ enabled: true, ttlDays: 30 });

    render(<SessionsSection />);

    expect(await screen.findByRole('checkbox', { name: 'Automatically archive inactive sessions and projects' })).toBeChecked();
    expect(screen.getByRole('spinbutton', { name: 'Archive inactive sessions and projects after days' })).toHaveValue(30);
  });

  it('can disable auto-archive and hides the TTL', async () => {
    mockDefaults();
    m.setAutoArchiveSettings.mockResolvedValue({ enabled: false, ttlDays: 7 });
    render(<SessionsSection />);

    const toggle = await screen.findByRole('checkbox', { name: 'Automatically archive inactive sessions and projects' });
    fireEvent.click(toggle);

    await waitFor(() => expect(m.setAutoArchiveSettings).toHaveBeenCalledWith({ enabled: false, ttlDays: 7 }));
    expect(screen.queryByRole('spinbutton', { name: 'Archive inactive sessions and projects after days' })).not.toBeInTheDocument();
  });

  it('saves a custom TTL', async () => {
    mockDefaults();
    m.setAutoArchiveSettings.mockResolvedValue({ enabled: true, ttlDays: 14 });
    render(<SessionsSection />);

    const input = await screen.findByRole('spinbutton', { name: 'Archive inactive sessions and projects after days' });
    fireEvent.change(input, { target: { value: '14' } });

    await waitFor(() => expect(m.setAutoArchiveSettings).toHaveBeenCalledWith({ enabled: true, ttlDays: 14 }));
  });

  it('prevents overlapping saves', async () => {
    mockDefaults();
    let resolveSave!: (value: { enabled: boolean; ttlDays: number }) => void;
    m.setAutoArchiveSettings.mockReturnValue(new Promise((resolve) => { resolveSave = resolve; }));
    render(<SessionsSection />);

    const input = await screen.findByRole('spinbutton', { name: 'Archive inactive sessions and projects after days' });
    fireEvent.change(input, { target: { value: '14' } });
    const toggle = screen.getByRole('checkbox', { name: 'Automatically archive inactive sessions and projects' });
    await waitFor(() => expect(toggle).toBeDisabled());

    expect(m.setAutoArchiveSettings).toHaveBeenCalledTimes(1);
    await act(async () => resolveSave({ enabled: true, ttlDays: 14 }));
  });
});
