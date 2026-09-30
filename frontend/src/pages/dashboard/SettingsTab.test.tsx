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
vi.mock('../../components/PluginSettings', () => ({ PluginSettings: () => <p>Plugin management</p> }));
vi.mock('../../components/MaintenanceSettings', () => ({ MaintenanceSettings: () => <p>Database maintenance</p> }));
vi.mock('./SettingsSections', () => ({
  NotificationsSection: () => null,
  SessionsSection: () => null,
}));
vi.mock('../../components/PreviewProviderSettings', () => ({ PreviewProviderSettings: () => null }));
vi.mock('../../components/PreviewAppSettings', () => ({ PreviewAppSettings: () => null }));
vi.mock('../../components/LinkPreviewSettings', () => ({ LinkPreviewSettings: () => null }));
vi.mock('./AutoApproveSection', () => ({ AutoApproveSection: () => null }));
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
  it('opens plugin management from the Settings navigation', () => {
    render(<SettingsTab />);
    expect(screen.queryByText('Plugin management')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Plugins' }));
    expect(screen.getByRole('heading', { name: 'Plugins' })).toBeVisible();
    expect(screen.getByText('Plugin management')).toBeVisible();
  });
  it('mounts maintenance only when its group is opened', () => {
    render(<SettingsTab />);
    expect(screen.queryByText('Database maintenance')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Maintenance' }));
    expect(screen.getByText('Database maintenance')).toBeVisible();
  });
  beforeEach(() => {
    logout.mockReset();
    promptInstall.mockReset();
  });

  it('searches settings and jumps to the matching group', () => {
    render(<SettingsTab />);
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search settings' }), { target: { value: 'disk space' } });
    fireEvent.click(screen.getByRole('button', { name: /Remove old diffs/ }));
    expect(screen.getByRole('searchbox', { name: 'Search settings' })).toHaveValue('');
    expect(screen.getByText('Database maintenance')).toBeVisible();

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search settings' }), { target: { value: 'nothing-like-this' } });
    expect(screen.getByRole('status')).toHaveTextContent('No settings match');
  });

  it('reopens the right tab when the same result is picked again', () => {
    render(<SettingsTab />);
    const search = () => {
      fireEvent.change(screen.getByRole('searchbox', { name: 'Search settings' }), { target: { value: 'Custom link rules' } });
      fireEvent.click(screen.getByRole('button', { name: /Custom link rules/ }));
    };
    search();
    expect(screen.getByRole('tab', { name: 'Link rules' })).toHaveAttribute('aria-selected', 'true');
    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Providers' }));
    fireEvent.click(screen.getByRole('tab', { name: 'Providers' }));
    expect(screen.getByRole('tab', { name: 'Providers' })).toHaveAttribute('aria-selected', 'true');
    search();
    expect(screen.getByRole('tab', { name: 'Link rules' })).toHaveAttribute('aria-selected', 'true');
  });

  it('explains why a conditional setting is missing', () => {
    // SessionsSection is mocked empty, as it renders with auto-archive off.
    render(<SettingsTab />);
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search settings' }), { target: { value: 'Archive after' } });
    expect(screen.getByRole('button', { name: /Archive after/ })).toHaveTextContent('automatic archiving is turned on');
    fireEvent.click(screen.getByRole('button', { name: /Archive after/ }));
    expect(screen.getByRole('status')).toHaveTextContent('Archive after is not shown right now. Shown only when automatic archiving is turned on.');
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
