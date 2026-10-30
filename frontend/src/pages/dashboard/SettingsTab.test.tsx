// @vitest-environment jsdom
import { act, fireEvent, render as renderComponent, screen, waitFor } from '@testing-library/react';
import type { ReactElement } from 'react';
import { createMemoryRouter, RouterProvider } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SettingsTab } from './SettingsTab';
import { useBetaFeatures } from '../../lib/betaFeatures';
import { postJSON } from '../../lib/api.requests';

vi.mock('../../lib/api.requests', async (original) => ({ ...await original<typeof import('../../lib/api.requests')>(), postJSON: vi.fn() }));

const logout = vi.fn();
const promptInstall = vi.fn();
const ios = vi.hoisted(() => ({ value: false }));
const openCode = vi.hoisted(() => ({ value: true }));
const device = vi.hoisted(() => ({ canInstall: true, installed: false }));
const account = vi.hoisted(() => ({ required: true }));
vi.mock('../../lib/useCapabilities', () => ({ useLocalOpenCode: () => openCode.value }));
vi.mock('../../lib/externalLinks', async (orig) => ({
  ...(await orig<typeof import('../../lib/externalLinks')>()),
  isIOS: () => ios.value,
}));

vi.mock('../../lib/headerContext', () => ({ usePageTitle: vi.fn() }));
vi.mock('../../components/upstream/PromptTemplateSettings', () => ({ PromptTemplateSettings: () => null }));
vi.mock('../../components/RemoteSettings', () => ({ RemoteSettings: () => null }));
vi.mock('../../components/SourceControlSettings', () => ({ SourceControlSettings: () => <p>Source-control setup</p> }));
vi.mock('../../components/SharingSettings', () => ({ SharingSettings: () => <p>Sharing controls</p> }));
vi.mock('../../components/PluginSettings', () => ({ PluginSettings: () => <p>Plugin management</p> }));
vi.mock('../../components/MaintenanceSettings', () => ({ MaintenanceSettings: () => <p>Database controls</p> }));
vi.mock('../../components/WebhookRelaySettings', () => ({ WebhookRelaySettings: () => null }));
vi.mock('./ProviderAccountSettings', () => ({ ProviderAccountSettings: () => null }));
vi.mock('../../components/HookPluginSettings', () => ({ HookPluginSettings: () => null }));
vi.mock('../../components/PlatformSettings', () => ({ PlatformSettings: () => null }));
vi.mock('../../components/IdleProjectSettings', () => ({ IdleProjectSettings: () => null }));
vi.mock('../../components/ApplicationHealthSettings', () => ({ ApplicationHealthSettings: () => <p>Application health controls</p> }));
vi.mock('../../components/SpeechSettings', () => ({ SpeechSettings: () => null }));
vi.mock('../../components/DefaultAgentSetting', () => ({ DefaultAgentSetting: () => null }));
vi.mock('./SessionAgentSettings', () => ({ ModelFallbackSettings: () => null, WorktreePermissionSettings: () => null }));
vi.mock('./SettingsSections', () => ({
  NotificationsSection: () => null,
  SessionsSection: () => null,
  ConversationSection: () => null,
}));
vi.mock('../../components/PreviewProviderSettings', () => ({ PreviewProviderSettings: () => null }));
vi.mock('../../components/PreviewAppSettings', () => ({ PreviewAppSettings: () => null }));
vi.mock('../../components/LinkPreviewSettings', () => ({ LinkPreviewSettings: () => null }));
vi.mock('./AutoApproveSection', () => ({ AutoApproveSection: () => null }));
vi.mock('../../lib/authStore', () => ({
  useAuthStore: (selector: (state: { authRequired: boolean; logout: typeof logout }) => unknown) =>
    selector({ authRequired: account.required, logout }),
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
  usePwaInstall: () => ({ ...device, promptInstall }),
}));

function render(ui: ReactElement, url = '/settings') {
  const router = createMemoryRouter([{ path: '/settings', element: ui }], { initialEntries: [url] });
  return { ...renderComponent(<RouterProvider router={router} />), router };
}

describe('SettingsTab actions', () => {
  it('offers a searchable, default-off Factory beta toggle in App settings', async () => {
    useBetaFeatures.setState({ factory: false, loaded: true, error: false });
    vi.mocked(postJSON).mockImplementation(async (_path, body) => body as never);
    const { unmount } = render(<SettingsTab />);
    expect(screen.getByRole('heading', { name: 'Beta features' })).toBeVisible();
    expect(screen.getByRole('checkbox', { name: 'Enable Factory beta' })).not.toBeChecked();
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search settings' }), { target: { value: 'Factory beta' } });
    fireEvent.click(screen.getByRole('button', { name: /Factory beta/ }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Enable Factory beta' }));
    await waitFor(() => expect(useBetaFeatures.getState().factory).toBe(true));
    unmount();
    render(<SettingsTab />);
    expect(screen.getByRole('checkbox', { name: 'Enable Factory beta' })).toBeChecked();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Enable Factory beta' }));
    await waitFor(() => expect(useBetaFeatures.getState().factory).toBe(false));
  });
  it('keeps application health in the System group instead of a global overlay', () => {
    render(<SettingsTab />);
    expect(screen.queryByText('Application health controls')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'System' }));
    expect(screen.getByRole('heading', { name: 'Application health', level: 3 })).toBeVisible();
    expect(screen.getByText('Application health controls')).toBeVisible();
  });
  it('groups settings into seven categories with visible sections', () => {
    render(<SettingsTab />);
    const navigation = screen.getByRole('navigation', { name: 'Settings groups' });
    expect([...navigation.querySelectorAll('button')].map((button) => button.textContent)).toEqual([
      'App & notifications', 'Sessions', 'Agents & models', 'Permissions', 'Connections & sharing', 'Plugins', 'System',
    ]);
    expect(screen.getByRole('heading', { name: 'Notifications', level: 3 })).toBeVisible();
    expect(screen.getByRole('heading', { name: 'This device', level: 3 })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Connections & sharing' }));
    for (const name of ['Remotes', 'Source control', 'Link previews', 'Public sharing', 'Webhooks']) {
      expect(screen.getByRole('heading', { name, level: 3 })).toBeVisible();
    }
  });
  it('restores the selected group and search from the URL after remounting', () => {
    const { router, unmount } = render(<SettingsTab />, '/settings?dir=project');
    fireEvent.click(screen.getByRole('button', { name: 'Connections & sharing' }));
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search settings' }), { target: { value: 'disk space & diffs' } });
    const url = `/settings${router.state.location.search}`;
    const params = new URLSearchParams(router.state.location.search);
    expect(params.get('tab')).toBe('connections');
    expect(params.get('q')).toBe('disk space & diffs');
    expect(params.get('dir')).toBe('project');
    unmount();
    render(<SettingsTab />, url);
    expect(screen.getByRole('searchbox', { name: 'Search settings' })).toHaveValue('disk space & diffs');
    fireEvent.keyDown(screen.getByRole('searchbox', { name: 'Search settings' }), { key: 'Escape' });
    expect(screen.getByText('Sharing controls')).toBeVisible();
  });

  it('clears the URL search when selecting a group or a search result', () => {
    const { router } = render(<SettingsTab />, '/settings?tab=plugins&q=disk+space&dir=project');
    fireEvent.change(screen.getByRole('combobox', { name: 'Settings group' }), { target: { value: 'connections' } });
    expect(router.state.location.search).toBe('?tab=connections&dir=project');
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search settings' }), { target: { value: 'disk space' } });
    fireEvent.click(screen.getByRole('button', { name: /Remove old diffs/ }));
    expect(router.state.location.search).toBe('?tab=system&dir=project');
  });

  it.each(['unknown', 'behaviour'])('falls back to the first visible group for tab=%s', (tab) => {
    render(<SettingsTab />, `/settings?tab=${tab}`);
    expect(screen.getByRole('combobox', { name: 'Settings group' })).toHaveValue('app');
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
    fireEvent.change(screen.getByRole('combobox', { name: 'Settings group' }), { target: { value: 'connections' } });
    expect(screen.getByRole('searchbox', { name: 'Search settings' })).toHaveValue('');
    expect(screen.getByText('Sharing controls')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Settings group' })).toHaveValue('connections');
  });
  it('opens plugin management from the Settings navigation', () => {
    render(<SettingsTab />);
    expect(screen.queryByText('Plugin management')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Plugins' }));
    expect(screen.getByRole('heading', { name: 'Plugins' })).toBeVisible();
    expect(screen.getByText('Plugin management')).toBeVisible();
  });
  it('opens the plugins category from discovery search results', () => {
    const { router } = render(<SettingsTab />);
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search settings' }), { target: { value: 'Discovery' } });
    fireEvent.click(screen.getByRole('button', { name: /Discovery/ }));
    expect(router.state.location.search).toBe('?tab=plugins');
    expect(screen.getByText('Plugin management')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Plugins' })).toHaveAttribute('aria-current', 'page');
  });
  it('lists only categories rather than individual feature sections', () => {
    render(<SettingsTab />);
    const groups = screen.getAllByRole('button').map((b) => b.textContent).filter((label) => label !== 'Install' && label !== 'Sign out');
    expect(groups).toEqual(['App & notifications', 'Sessions', 'Agents & models', 'Permissions', 'Connections & sharing', 'Plugins', 'System']);
  });
  it('keeps always-mounted feature sections mounted while hidden', () => {
    render(<SettingsTab />);
    expect(screen.getByText('Sharing controls')).not.toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Connections & sharing' }));
    expect(screen.getByRole('heading', { name: 'Public sharing' })).toBeVisible();
    expect(screen.getByText('Sharing controls')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Sessions' }));
    expect(screen.getByText('Sharing controls')).not.toBeVisible();
  });
  it('hides the OpenCode maintenance group without OpenCode', () => {
    openCode.value = false;
    try {
      render(<SettingsTab />);
      fireEvent.click(screen.getByRole('button', { name: 'System' }));
      expect(screen.queryByRole('heading', { name: 'Database maintenance' })).not.toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Resource management' })).toBeVisible();
    } finally {
      openCode.value = true;
    }
  });
  it('mounts maintenance only when its group is opened', () => {
    render(<SettingsTab />);
    expect(screen.queryByText('Database controls')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'System' }));
    expect(screen.getByText('Database controls')).toBeVisible();
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
    expect(screen.getByText('Database controls')).toBeVisible();

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

    expect(screen.getByRole('button', { name: 'App & notifications' })).toHaveClass('oc-button');
    expect(screen.getByRole('button', { name: 'Install' })).toHaveClass('oc-button');
    fireEvent.click(screen.getByRole('button', { name: 'Install' }));
    expect(promptInstall).toHaveBeenCalledOnce();

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
    const toggle = screen.getByRole('checkbox', { name: 'Open external links in Chrome' });
    expect(toggle).not.toBeChecked();
    fireEvent.click(toggle);
    expect(toggle).toBeChecked();
    expect(localStorage.getItem('ocman:open-links-in-chrome')).toBe('1');
    ios.value = false;
    localStorage.clear();
  });

  it.each([
    ['notifications', 'app'], ['behaviour', 'app'], ['account', 'app'],
    ['conversation', 'sessions'], ['templates', 'sessions'],
    ['platforms', 'agents'], ['provider-accounts', 'agents'],
    ['auto-approve', 'permissions'], ['remotes', 'connections'], ['plugins', 'plugins'],
    ['sharing', 'connections'], ['webhooks', 'connections'], ['link-previews', 'connections'],
    ['maintenance', 'system'], ['idle-projects', 'system'],
  ])('keeps the legacy %s link opening %s', (tab, category) => {
    render(<SettingsTab />, `/settings?tab=${tab}`);
    expect(screen.getByRole('combobox', { name: 'Settings group' })).toHaveValue(category);
  });

  it('hides empty device and account sections', () => {
    device.canInstall = false;
    account.required = false;
    try {
      render(<SettingsTab />);
      expect(screen.queryByRole('heading', { name: 'This device' })).not.toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'Account' })).not.toBeInTheDocument();
    } finally {
      device.canInstall = true;
      account.required = true;
    }
  });

  it('shows the installed state without offering another installation', () => {
    device.installed = true;
    try {
      render(<SettingsTab />);
      expect(screen.getByRole('button', { name: 'Installed' })).toBeDisabled();
    } finally {
      device.installed = false;
    }
  });
});
