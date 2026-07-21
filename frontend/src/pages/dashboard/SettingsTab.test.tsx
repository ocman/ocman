// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SettingsTab } from './SettingsTab';

const logout = vi.fn();
const promptInstall = vi.fn();

vi.mock('../../lib/headerContext', () => ({ usePageTitle: vi.fn() }));
vi.mock('../../components/upstream/PromptTemplateSettings', () => ({ PromptTemplateSettings: () => null }));
vi.mock('../../components/RemoteSettings', () => ({ RemoteSettings: () => null }));
vi.mock('../../components/SharingSettings', () => ({ SharingSettings: () => null }));
vi.mock('./SettingsSections', () => ({
  NotificationsSection: () => null,
  SessionsSection: () => null,
  AutoApproveSection: () => null,
}));
vi.mock('../../lib/authStore', () => ({
  useAuthStore: (selector: (state: { authRequired: boolean; logout: typeof logout }) => unknown) =>
    selector({ authRequired: true, logout }),
}));
vi.mock('../../lib/uiStore', () => ({
  useUiStore: (selector: (state: Record<string, unknown>) => unknown) => selector({
    setPromptSections: vi.fn(),
    setAutoApproveDelayMs: vi.fn(),
  }),
}));
vi.mock('../../lib/apiStore', () => ({
  useApiStore: (selector: (state: Record<string, unknown>) => unknown) => selector({
    getPromptSections: vi.fn().mockResolvedValue([]),
    getJudgeDelay: vi.fn().mockResolvedValue(0),
  }),
}));
vi.mock('../../lib/usePwaInstall', () => ({
  usePwaInstall: () => ({ canInstall: true, installed: false, promptInstall }),
}));

describe('SettingsTab actions', () => {
  beforeEach(() => {
    logout.mockReset();
    promptInstall.mockReset();
  });

  it('keeps install and sign-out actions working inside setting rows', () => {
    render(<SettingsTab />);

    fireEvent.click(screen.getByRole('button', { name: 'App' }));
    fireEvent.click(screen.getByRole('button', { name: 'Install' }));
    expect(promptInstall).toHaveBeenCalledOnce();

    fireEvent.click(screen.getByRole('button', { name: 'Account' }));
    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    expect(logout).toHaveBeenCalledOnce();
  });
});
