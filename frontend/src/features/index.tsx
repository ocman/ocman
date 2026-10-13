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
import { FactoryConfiguration, FactoryEpicDetail, FactoryEpics, FactoryHowTo, FactoryOverview, FactoryQueue } from '../pages/Factory';
import { FactoryIssues } from '../pages/FactoryIssues';
import { Routines } from '../pages/Routines';
import { Artifacts } from '../pages/Artifacts';
import { ArtifactDetail } from '../pages/ArtifactDetail';
import { PluginSettings } from '../components/PluginSettings';
import { AutoApproveSection } from '../pages/dashboard/AutoApproveSection';
import { SharingSettings } from '../components/SharingSettings';
import { WebhookRelaySettings } from '../components/WebhookRelaySettings';

export interface FeatureRoute {
  path: string;
  element: ReactNode;
  /** `dashboard` pages share DashboardLayout; `page` pages only the padding. */
  layout: 'dashboard' | 'page';
  /** Header title for this exact path. */
  title?: string;
}

export interface Feature {
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
  settings?: { id: SettingsGroupId; label: string; mount: 'always' | 'active'; Section: ComponentType };
}

export const FEATURES: Feature[] = [
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
  { id: 'plugins', settings: { id: 'plugins', label: 'Plugins', mount: 'active', Section: PluginSettings } },
  { id: 'auto-approve', settings: { id: 'auto-approve', label: 'Auto-approve', mount: 'always', Section: AutoApproveSection } },
  { id: 'sharing', settings: { id: 'sharing', label: 'Sharing', mount: 'always', Section: SharingSettings } },
  { id: 'webhooks', settings: { id: 'webhooks', label: 'Webhooks', mount: 'active', Section: WebhookRelaySettings } },
];

export const featureRoutes = (layout: FeatureRoute['layout']) =>
  FEATURES.flatMap((f) => (f.routes ?? []).filter((r) => r.layout === layout));

export const featureNav = FEATURES.flatMap((f) => (f.nav ? [f.nav] : []));

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
