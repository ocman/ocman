import { featureNav } from '../features';
import type { BetaFeature } from '../lib/betaFeatures';

// activePrefix: sub-pages living outside the link's own path (singular routes, sibling tabs).
export const NAV_ITEMS: { to: string; label: string; icon: string; activePrefix?: string; beta?: BetaFeature }[] = [
  { to: '/', label: 'Home', icon: 'bi-house', activePrefix: '/session/' },
  { to: '/sessions', label: 'Sessions', icon: 'bi-collection' },
  { to: '/projects', label: 'Projects', icon: 'bi-folder', activePrefix: '/project/' },
  ...featureNav,
  { to: '/analytics', label: 'Analytics', icon: 'bi-bar-chart' },
];

/** Destinations rendered in the navigation footer rather than NAV_ITEMS. Activity is a panel, not a page. */
export const FOOTER_DESTINATIONS: { to: string; label: string }[] = [
  { to: '/inbox', label: 'Inbox' },
  { to: '/sessions?activity=resources', label: 'Activity' },
  { to: '/provider-usage', label: 'Provider usage' },
  { to: '/settings', label: 'Settings' },
];
