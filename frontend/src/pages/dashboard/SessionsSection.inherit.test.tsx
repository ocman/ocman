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
  fetchJSON: vi.fn().mockResolvedValue({ defaultAgent: 'build' }),
  postJSON: vi.fn(),
  api: {
    getWorktreeInheritPermissions: vi.fn(),
    setWorktreeInheritPermissions: vi.fn(),
    getAutoArchiveSettings: vi.fn(),
    setAutoArchiveSettings: vi.fn(),
    getModelFallthroughSettings: vi.fn(),
    setModelFallthroughSettings: vi.fn(),
    getArchiveResurface: vi.fn().mockResolvedValue({ mode: 'halt' }),
    setArchiveResurface: vi.fn(),
  },
}));
import { api } from '../../lib/api';
const m = api as unknown as Record<string, ReturnType<typeof vi.fn>>;

afterEach(() => {
  vi.clearAllMocks();
  useUiStore.getState().setShowMessageMetadata(false);
});

function mockDefaults() {
  m.getArchiveResurface.mockResolvedValue({ mode: 'halt' });
  m.getWorktreeInheritPermissions.mockResolvedValue({ enabled: true });
  m.getAutoArchiveSettings.mockResolvedValue({ enabled: true, ttlDays: 7 });
  m.getModelFallthroughSettings.mockResolvedValue({ patienceMinutes: 5, fallbackMinutes: 15 });
}

describe('Archived session resurfacing', () => {
  it('keeps the setting disabled if loading fails', async () => {
    mockDefaults();
    m.getArchiveResurface.mockRejectedValue(new Error('offline'));
    render(<SessionsSection />);
    await act(async () => {});
    expect(screen.getByRole('combobox', { name: 'Show archived sessions again' })).toBeDisabled();
  });

  it('keeps the saved choice if saving fails', async () => {
    mockDefaults();
    m.getArchiveResurface.mockResolvedValue({ mode: 'activity' });
    m.setArchiveResurface.mockRejectedValue(new Error('offline'));
    render(<SessionsSection />);
    const select = screen.getByRole('combobox', { name: 'Show archived sessions again' });
    await waitFor(() => expect(select).toBeEnabled());
    expect(select).toHaveTextContent('Any activity');
    fireEvent.click(select);
    fireEvent.click(screen.getByRole('option', { name: 'Session halts (default)' }));
    await waitFor(() => expect(m.setArchiveResurface).toHaveBeenCalledWith('halt'));
    await waitFor(() => expect(select).toBeEnabled());
    expect(select).toHaveTextContent('Any activity');
  });

  it('loads the default and saves any activity', async () => {
    mockDefaults();
    m.setArchiveResurface.mockResolvedValue({ mode: 'activity' });
    render(<SessionsSection />);
    const select = screen.getByRole('combobox', { name: 'Show archived sessions again' });
    await waitFor(() => expect(select).toBeEnabled());
    expect(select).toHaveTextContent('Session halts (default)');
    fireEvent.click(select);
    fireEvent.click(screen.getByRole('option', { name: 'Any activity' }));
    await waitFor(() => expect(m.setArchiveResurface).toHaveBeenCalledWith('activity'));
    await waitFor(() => expect(select).toHaveTextContent('Any activity'));
  });
});

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
    m.getModelFallthroughSettings.mockReturnValue(new Promise(() => {}));

    render(<SessionsSection />);

    expect(screen.getByRole('checkbox', { name: 'Automatically archive inactive sessions and projects' })).toBeDisabled();
    expect(screen.queryByRole('spinbutton', { name: 'Archive inactive sessions and projects after days' })).not.toBeInTheDocument();
  });

  it('loads the toggle and TTL', async () => {
    mockDefaults();
    m.getAutoArchiveSettings.mockResolvedValue({ enabled: true, ttlDays: 30 });

    render(<SessionsSection />);

    // The toggle renders checked (disabled) before settings load, so wait
    // for the TTL field, which only appears once they have.
    expect(await screen.findByRole('spinbutton', { name: 'Archive inactive sessions and projects after days' })).toHaveValue(30);
    expect(screen.getByRole('checkbox', { name: 'Automatically archive inactive sessions and projects' })).toBeChecked();
  });

  it('can disable auto-archive and hides the TTL', async () => {
    mockDefaults();
    m.setAutoArchiveSettings.mockResolvedValue({ enabled: false, ttlDays: 7 });
    render(<SessionsSection />);

    const toggle = await screen.findByRole('checkbox', { name: 'Automatically archive inactive sessions and projects' });
    // A click on the still-disabled toggle (settings not loaded yet) is ignored.
    await waitFor(() => expect(toggle).toBeEnabled());
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

describe('SessionsSection model fallthrough thresholds', () => {
  it('loads and saves both thresholds', async () => {
    mockDefaults();
    m.setModelFallthroughSettings.mockResolvedValue({ patienceMinutes: 2, fallbackMinutes: 15 });
    render(<SessionsSection />);

    const patience = await screen.findByRole('spinbutton', { name: 'Model fallthrough patience in minutes' });
    expect(patience).toHaveValue(5);
    expect(screen.getByRole('spinbutton', { name: 'Model fallthrough cooldown in minutes' })).toHaveValue(15);

    fireEvent.change(patience, { target: { value: '2' } });
    await waitFor(() => expect(m.setModelFallthroughSettings).toHaveBeenCalledWith({ patienceMinutes: 2, fallbackMinutes: 15 }));
  });

  it('reverts when the save fails', async () => {
    mockDefaults();
    m.setModelFallthroughSettings.mockRejectedValue(new Error('boom'));
    render(<SessionsSection />);

    const cooldown = await screen.findByRole('spinbutton', { name: 'Model fallthrough cooldown in minutes' });
    fireEvent.change(cooldown, { target: { value: '30' } });
    await waitFor(() => expect(m.setModelFallthroughSettings).toHaveBeenCalled());
    await waitFor(() => expect(cooldown).toHaveValue(15));
  });
});
