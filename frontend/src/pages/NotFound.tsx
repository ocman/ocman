import { Link, useLocation } from 'react-router-dom';
import { PageState } from '../components/PageState';
import { RouteButton } from '../components/Control';
import { FOOTER_DESTINATIONS, NAV_ITEMS } from '../components/mainNavItems';
import { suggestDestination, type Destination } from '../lib/suggestDestination';
import { useBetaFeatures } from '../lib/betaFeatures';
import { usePageTitle } from '../lib/headerContext';
import styles from './NotFound.module.css';

/** Catch-all for routes no page owns: suggest the likely destination and list the rest. */
export function NotFound() {
  usePageTitle('Page not found');
  const { pathname } = useLocation();
  const beta = useBetaFeatures();
  const destinations: Destination[] = [
    ...NAV_ITEMS.filter((item) => item.to !== '/' && (!item.beta || beta[item.beta])),
    ...FOOTER_DESTINATIONS,
  ];
  const suggestion = suggestDestination(pathname, destinations);
  return <PageState icon="bi-signpost-split" title="Page not found" description={<>
    Nothing lives at <code>{pathname}</code>.{suggestion && <> Did you mean <strong>{suggestion.label}</strong>?</>}
  </>}>
    <div className={styles.actions}>
      {suggestion && <RouteButton variant="accent" to={suggestion.to}>Open {suggestion.label}</RouteButton>}
      <RouteButton to="/">Go to Home</RouteButton>
    </div>
    <nav aria-label="Other pages" className={styles.links}>
      {destinations.filter((d) => d !== suggestion).map((d) => <Link key={d.to} to={d.to}>{d.label}</Link>)}
    </nav>
  </PageState>;
}
