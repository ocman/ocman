// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import { SessionsSection } from './SettingsSections';

// Only the worktree-inherit-permissions toggle (#101) is under test here;
// mock the api so the toggle loads and saves against known values. The
// other rows in the section read uiStore defaults and are harmless.
vi.mock('../../lib/api', () => ({
  api: {
    getWorktreeInheritPermissions: vi.fn(),
    setWorktreeInheritPermissions: vi.fn(),
  },
}));
import { api } from '../../lib/api';
const m = api as unknown as Record<string, ReturnType<typeof vi.fn>>;

afterEach(() => vi.clearAllMocks());

describe('SessionsSection worktree inherit toggle (#101)', () => {
  it('reflects the loaded enabled state', async () => {
    m.getWorktreeInheritPermissions.mockResolvedValue({ enabled: false });
    render(<SessionsSection />);
    const toggle = await screen.findByTestId('worktree-inherit-toggle');
    await waitFor(() => expect((toggle as HTMLInputElement).checked).toBe(false));
  });

  it('persists the toggle when switched off', async () => {
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
