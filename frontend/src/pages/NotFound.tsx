import { useLocation } from 'react-router-dom';
import { PageState } from '../components/PageState';
import { RouteButton } from '../components/Control';
import { usePageTitle } from '../lib/headerContext';

/** Catch-all for routes no page owns, so a mistyped or stale link is never blank. */
export function NotFound() {
  usePageTitle('Page not found');
  const { pathname } = useLocation();
  return <PageState icon="bi-signpost-split" title="Page not found" description={<>Nothing lives at <code>{pathname}</code>. The link may be mistyped or out of date.</>}>
    <RouteButton to="/">Go to Home</RouteButton>
  </PageState>;
}
