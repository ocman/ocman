import type { ReactNode } from 'react';
import { useBetaFeatures, type BetaFeature } from '../lib/betaFeatures';
import { RouteButton } from './Control';
import { PageState } from './PageState';

export function BetaFeatureGate({ feature, children }: { feature: BetaFeature; children: ReactNode }) {
  const enabled = useBetaFeatures((s) => s[feature]);
  if (enabled) return children;
  return <PageState icon="bi-buildings" title="Factory is in beta"
    description="Enable Factory in Settings → App → Beta features to use it in this browser.">
    <RouteButton to="/settings?tab=app#setting-factory-beta">Open beta settings</RouteButton>
  </PageState>;
}
