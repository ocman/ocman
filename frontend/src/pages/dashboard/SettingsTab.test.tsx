// @vitest-environment jsdom
import { act, fireEvent, render as renderComponent, screen } from '@testing-library/react';
import type { ReactElement } from 'react';
import { createMemoryRouter, RouterProvider } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SettingsTab } from './SettingsTab';

const logout = vi.fn();
const promptInstall = vi.fn();
const ios = vi.hoisted(() => ({ value: false }));
const openCode = vi.hoisted(() => ({ value: true }));
vi.mock('../../lib/useCapabilities', () => ({ useLocalOpenCode: () => openCode.value }));
vi.mock('../../lib/externalLinks', async (orig) => ({
  ...(await orig<typeof import('../../lib/externalLinks')>()),
  isIOS: () => ios.value,
}));

vi.mock('../../lib/headerContext', () => ({ usePageTitle: vi.fn() }));
vi.mock('../../components/upstream/PromptTemplateSettings', () => ({ PromptTemplateSettings: () => null }));
vi.mock('../../components/RemoteSettings', () => ({ RemoteSettings: () => null }));
vi.mock('../../components/SharingSettings', () => ({ SharingSettings: () => <p>Sharing controls</p> }));
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

function render(ui: ReactElement, url = '/settings') {
  const router = createMemoryRouter([{ path: '/settings', element: ui }], { initialEntries: [url] });
  return { ...renderComponent(<RouterProvider router={router} />), router };
}

describe('SettingsTab actions', () => {
  it('restores the selected group and search from the URL after remounting', () => {
    const { router, unmount } = render(<SettingsTab />, '/settings?dir=project');
    fireEvent.click(screen.getByRole('button', { name: 'Plugins' }));
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search settings' }), { target: { value: 'disk space & diffs' } });
    const url = `/settings${router.state.location.search}`;
    const params = new URLSearchParams(router.state.location.search);
    expect(params.get('tab')).toBe('plugins');
    expect(params.get('q')).toBe('disk space & diffs');
    expect(params.get('dir')).toBe('project');
    unmount();
    render(<SettingsTab />, url);
    expect(screen.getByRole('searchbox', { name: 'Search settings' })).toHaveValue('disk space & diffs');
    fireEvent.keyDown(screen.getByRole('searchbox', { name: 'Search settings' }), { key: 'Escape' });
    expect(screen.getByText('Plugin management')).toBeVisible();
  });

  it('clears the URL search when selecting a group or a search result', () => {
    const { router } = render(<SettingsTab />, '/settings?tab=plugins&q=disk+space&dir=project');
    fireEvent.change(screen.getByRole('combobox', { name: 'Settings group' }), { target: { value: 'sharing' } });
    expect(router.state.location.search).toBe('?tab=sharing&dir=project');
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search settings' }), { target: { value: 'disk space' } });
    fireEvent.click(screen.getByRole('button', { name: /Remove old diffs/ }));
    expect(router.state.location.search).toBe('?tab=maintenance&dir=project');
  });

  it.each(['unknown', 'behaviour'])('falls back to the first visible group for tab=%s', (tab) => {
    render(<SettingsTab />, `/settings?tab=${tab}`);
    expect(screen.getByRole('combobox', { name: 'Settings group' })).toHaveValue('notifications');
  });

  it('follows URL navigation and replaces history while editing search', async () => {
    const { router } = render(<SettingsTab />, '/settings?tab=plugins');
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search settings' }), { target: { value: 'disk space' } });
    expect(router.state.historyAction).toBe('REPLACE');
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search settings' }), { target: { value: '' } });
    expect(router.state.location.search).toBe('?tab=plugins');
    await act(() => router.navigate('/settings?tab=sharing&q=custom'));
    expect(screen.getByRole('searchbox', { name: 'Search settings' })).toHaveValue('custom');
    await act(() => router.navigate(-1));
    expect(screen.getByRole('searchbox', { name: 'Search settings' })).toHaveValue('');
    expect(screen.getByText('Plugin management')).toBeVisible();
  });

  it('selects a group from the compact navigation and clears the search view', () => {
    render(<SettingsTab />);
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search settings' }), { target: { value: 'disk space' } });
    fireEvent.change(screen.getByRole('combobox', { name: 'Settings group' }), { target: { value: 'plugins' } });
    expect(screen.getByRole('searchbox', { name: 'Search settings' })).toHaveValue('');
    expect(screen.getByText('Plugin management')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Settings group' })).toHaveValue('plugins');
  });
  it('opens plugin management from the Settings navigation', () => {
    render(<SettingsTab />);
    expect(screen.queryByText('Plugin management')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Plugins' }));
    expect(screen.getByRole('heading', { name: 'Plugins' })).toBeVisible();
    expect(screen.getByText('Plugin management')).toBeVisible();
  });
  it('lists feature groups in their fixed slot among the core groups', () => {
    render(<SettingsTab />);
    const groups = screen.getAllByRole('button').map((b) => b.textContent).filter((label) => label !== 'Install' && label !== 'Sign out');
    expect(groups).toEqual(['Notifications', 'Sessions', 'Remotes', 'Plugins', 'Platforms', 'Idle projects', 'Auto-approve', 'Sharing', 'Webhooks',
      'PR & Issue templates', 'Link previews', 'Maintenance', 'Provider accounts', 'App', 'Account']);
  });
  it('keeps always-mounted feature sections mounted while hidden', () => {
    render(<SettingsTab />);
    expect(screen.getByText('Sharing controls')).not.toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Sharing' }));
    expect(screen.getByRole('heading', { name: 'Sharing' })).toBeVisible();
    expect(screen.getByText('Sharing controls')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Plugins' }));
    expect(screen.getByText('Sharing controls')).not.toBeVisible();
  });
  it('hides the OpenCode maintenance group without OpenCode', () => {
    openCode.value = false;
    try {
      render(<SettingsTab />);
      expect(screen.queryByRole('button', { name: 'Maintenance' })).not.toBeInTheDocument();
    } finally {
      openCode.value = true;
    }
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

    expect(screen.getByRole('button', { name: 'App' })).toHaveClass('oc-button');
    fireEvent.click(screen.getByRole('button', { name: 'App' }));
    expect(screen.getByRole('button', { name: 'Install' })).toHaveClass('oc-button');
    fireEvent.click(screen.getByRole('button', { name: 'Install' }));
    expect(promptInstall).toHaveBeenCalledOnce();

    fireEvent.click(screen.getByRole('button', { name: 'Account' }));
    expect(screen.getByRole('button', { name: 'Sign out' })).toHaveClass('oc-button');
    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    expect(logout).toHaveBeenCalledOnce();
  });

  it('shows the Behaviour tab and Chrome toggle only on iOS', async () => {
    ios.value = false;
    const { unmount } = render(<SettingsTab />);
    expect(screen.queryByRole('button', { name: 'Behaviour' })).not.toBeInTheDocument();
    unmount();

    ios.value = true;
    render(<SettingsTab />);
    fireEvent.click(screen.getByRole('button', { name: 'Behaviour' }));
    const toggle = screen.getByRole('checkbox', { name: 'Open external links in Chrome' });
    expect(toggle).not.toBeChecked();
    fireEvent.click(toggle);
    expect(toggle).toBeChecked();
    expect(localStorage.getItem('ocman:open-links-in-chrome')).toBe('1');
    ios.value = false;
    localStorage.clear();
  });
});
