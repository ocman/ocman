/**
 * Static registry of the feature pages and settings sections that used to
 * be listed by hand in App's routes, the main nav, routeTitle and the
 * Settings page. Adding a feature means adding one entry here; the shells
 * keep core pages (sessions, projects, analytics, inbox) to themselves.
 *
 * Order matters: nav links and settings groups render in list order, at a
 * fixed slot between the core entries around them.
 */
import type { ComponentType, ReactNode } from 'react';
import { Navigate } from 'react-router-dom';
import type { SettingsGroupId } from '../lib/settingsCatalog';
import { lazyComponent } from '../components/lazyComponent';
import { Routines } from '../pages/Routines';
import { Artifacts } from '../pages/Artifacts';
import { ArtifactDetail } from '../pages/ArtifactDetail';
import { PluginSettings } from '../components/PluginSettings';
import { AutoApproveSection } from '../pages/dashboard/AutoApproveSection';
import { SharingSettings } from '../components/SharingSettings';
import { WebhookRelaySettings } from '../components/WebhookRelaySettings';
import { IdleProjectSettings } from '../components/IdleProjectSettings';
import { PlatformSettings } from '../components/PlatformSettings';
import { ConversationSection, NotificationsSection, SessionsSection } from '../pages/dashboard/SettingsSections';
import { AccountSettings, DeviceSettings, LinkPreviewSection, NewSessionSettings, RemoteSection } from '../pages/dashboard/CoreSettingsSections';
import type { RevealRequest } from '../lib/useRevealSetting';
import { ProviderAccountSettings } from '../pages/dashboard/ProviderAccountSettings';
import { ModelFallbackSettings, WorktreePermissionSettings } from '../pages/dashboard/SessionAgentSettings';
import { SpeechSettings } from '../components/SpeechSettings';
import { HookPluginSettings } from '../components/HookPluginSettings';
import { MaintenanceSettings } from '../components/MaintenanceSettings';
import { PluginPage } from '../pages/PluginPage';
import { PluginPageNav } from '../components/PluginPageNav';
import { BackgroundTasks } from '../pages/BackgroundTasks';
import { ApplicationHealthSettings } from '../components/ApplicationHealthSettings';

// Factory pages pull in the epic graph (dagre): load them with the route.
const factoryPages = () => import('../pages/Factory');
const FactoryOverview = lazyComponent(() => factoryPages().then((m) => m.FactoryOverview));
const FactoryHowTo = lazyComponent(() => factoryPages().then((m) => m.FactoryHowTo));
const FactoryEpics = lazyComponent(() => factoryPages().then((m) => m.FactoryEpics));
const FactoryEpicDetail = lazyComponent(() => factoryPages().then((m) => m.FactoryEpicDetail));
const FactoryIssues = lazyComponent(() => import('../pages/FactoryIssues').then((m) => m.FactoryIssues));
const FactoryQueue = lazyComponent(() => factoryPages().then((m) => m.FactoryQueue));
const FactoryConfiguration = lazyComponent(() => factoryPages().then((m) => m.FactoryConfiguration));

export interface FeatureRoute {
  path: string;
  element: ReactNode;
  /** `dashboard` pages share DashboardLayout; `page` pages only the padding. */
  layout: 'dashboard' | 'page';
  /** Header title for this exact path. */
  title?: string;
}

export interface Feature {
  Navigation?: ComponentType<{ onNavigate?: () => void; replace?: boolean }>;
  id: string;
  routes?: FeatureRoute[];
  /** Header title for every other path under this prefix. */
  titlePrefix?: { prefix: string; title: string };
  /** activePrefix also marks the link active for sub-pages outside `to`. */
  nav?: { to: string; label: string; icon: string; activePrefix?: string };
  /**
   * `always` sections stay mounted (hidden) while Settings is open, so
   * unsaved edits survive switching groups; `active` ones mount on demand.
   */
  settings?: {
    id: string;
    category: SettingsGroupId;
    label: string;
    mount: 'always' | 'active';
    Section: ComponentType<{ target?: RevealRequest | null }>;
    visible?: 'device' | 'account' | 'maintenance';
    /** Sections with conditional or multiple headings render their own frame. */
    ownSections?: boolean;
  };
}

export const FEATURES: Feature[] = [
  { id: 'plugin-pages', routes: [{ path: '/plugins/:pluginId/:pageId', element: <PluginPage />, layout: 'page', title: 'Plugin workspace' }], Navigation: PluginPageNav, titlePrefix: { prefix: '/plugins/', title: 'Plugin workspace' } },
  {
    id: 'background-tasks',
    routes: [{ path: '/background-tasks', element: <BackgroundTasks />, layout: 'dashboard', title: 'Background tasks' }],
  },
  {
    id: 'factory',
    routes: [
      { path: '/factory', element: <Navigate to="/factory/overview" replace />, layout: 'page' },
      { path: '/factory/overview', element: <FactoryOverview />, layout: 'page' },
      { path: '/factory/how-to', element: <FactoryHowTo />, layout: 'page', title: 'Factory how to' },
      { path: '/factory/epics', element: <FactoryEpics />, layout: 'page' },
      { path: '/factory/epics/:id', element: <FactoryEpicDetail />, layout: 'page' },
      { path: '/factory/issues/:issueId?', element: <FactoryIssues />, layout: 'page' },
      { path: '/factory/queue', element: <FactoryQueue />, layout: 'page', title: 'Factory queue' },
      { path: '/factory/configuration', element: <FactoryConfiguration />, layout: 'page', title: 'Factory configuration' },
    ],
    titlePrefix: { prefix: '/factory/', title: 'Factory' },
    nav: { to: '/factory/overview', label: 'Factory', icon: 'bi-buildings', activePrefix: '/factory/' },
  },
  {
    id: 'routines',
    routes: [{ path: '/routines', element: <Routines />, layout: 'dashboard', title: 'Routines' }],
    nav: { to: '/routines', label: 'Routines', icon: 'bi-clock-history' },
  },
  {
    id: 'artifacts',
    routes: [
      { path: '/artifacts', element: <Artifacts />, layout: 'dashboard', title: 'Artifacts' },
      { path: '/artifacts/:id', element: <ArtifactDetail />, layout: 'dashboard' },
    ],
    titlePrefix: { prefix: '/artifacts/', title: 'Artifacts' },
    nav: { to: '/artifacts', label: 'Artifacts', icon: 'bi-box-seam' },
  },
  { id: 'notifications', settings: { id: 'notifications', category: 'app', label: 'Notifications', mount: 'always', Section: NotificationsSection } },
  { id: 'device', settings: { id: 'device', category: 'app', label: 'This device', mount: 'always', Section: DeviceSettings, visible: 'device' } },
  { id: 'account', settings: { id: 'account', category: 'app', label: 'Account', mount: 'always', Section: AccountSettings, visible: 'account' } },
  { id: 'new-sessions', settings: { id: 'new-sessions', category: 'sessions', label: 'New sessions', mount: 'always', Section: NewSessionSettings } },
  { id: 'conversation', settings: { id: 'conversation', category: 'sessions', label: 'Conversation', mount: 'active', Section: ConversationSection } },
  { id: 'voice', settings: { id: 'voice', category: 'sessions', label: 'Voice', mount: 'active', Section: SpeechSettings } },
  { id: 'session-history', settings: { id: 'session-history', category: 'sessions', label: 'History & archiving', mount: 'always', Section: SessionsSection } },
  { id: 'platforms', settings: { id: 'platforms', category: 'agents', label: 'Platforms', mount: 'active', Section: PlatformSettings } },
  { id: 'provider-accounts', settings: { id: 'provider-accounts', category: 'agents', label: 'Provider accounts', mount: 'active', Section: ProviderAccountSettings } },
  { id: 'hooks', settings: { id: 'hooks', category: 'agents', label: 'Hooks', mount: 'active', Section: HookPluginSettings, ownSections: true } },
  { id: 'model-fallback', settings: { id: 'model-fallback', category: 'agents', label: 'Model fallback', mount: 'active', Section: ModelFallbackSettings } },
  { id: 'auto-approve', settings: { id: 'auto-approve', category: 'permissions', label: 'Auto-approve', mount: 'always', Section: AutoApproveSection, ownSections: true } },
  { id: 'worktrees', settings: { id: 'worktrees', category: 'permissions', label: 'Worktrees', mount: 'active', Section: WorktreePermissionSettings } },
  { id: 'remotes', settings: { id: 'remotes', category: 'connections', label: 'Remotes', mount: 'always', Section: RemoteSection } },
  { id: 'plugins', settings: { id: 'plugins', category: 'plugins', label: 'Plugins', mount: 'active', Section: PluginSettings, ownSections: true } },
  { id: 'link-previews', settings: { id: 'link-previews', category: 'connections', label: 'Link previews', mount: 'active', Section: LinkPreviewSection } },
  { id: 'sharing', settings: { id: 'sharing', category: 'connections', label: 'Public sharing', mount: 'always', Section: SharingSettings } },
  { id: 'webhooks', settings: { id: 'webhooks', category: 'connections', label: 'Webhooks', mount: 'active', Section: WebhookRelaySettings } },
  { id: 'idle-projects', settings: { id: 'idle-projects', category: 'system', label: 'Resource management', mount: 'active', Section: IdleProjectSettings } },
  { id: 'application-health', settings: { id: 'application-health', category: 'system', label: 'Application health', mount: 'active', Section: ApplicationHealthSettings } },
  { id: 'maintenance', settings: { id: 'maintenance', category: 'system', label: 'Database maintenance', mount: 'active', Section: MaintenanceSettings, visible: 'maintenance' } },
];

export const featureRoutes = (layout: FeatureRoute['layout']) =>
  FEATURES.flatMap((f) => (f.routes ?? []).filter((r) => r.layout === layout));

export const featureNav = FEATURES.flatMap((f) => (f.nav ? [f.nav] : []));
export const featureNavigation = FEATURES.flatMap((f) => f.Navigation ? [f.Navigation] : []);

export const featureSettings = FEATURES.flatMap((f) => (f.settings ? [f.settings] : []));

/** Header title a feature gives `path`, or undefined when no feature owns it. */
export function featureTitle(path: string): string | undefined {
  for (const feature of FEATURES) {
    const route = feature.routes?.find((r) => r.path === path && r.title);
    if (route) return route.title;
    if (feature.titlePrefix && path.startsWith(feature.titlePrefix.prefix)) return feature.titlePrefix.title;
  }
  return undefined;
}
