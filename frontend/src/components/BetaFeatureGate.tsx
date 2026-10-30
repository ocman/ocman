import type { ReactNode } from 'react';
import { useBetaFeatures, type BetaFeature } from '../lib/betaFeatures';
import { RouteButton } from './Control';
import { PageState } from './PageState';
import { usePageTitle } from '../lib/headerContext';

export function BetaFeatureGate({ feature, children }: { feature: BetaFeature; children: ReactNode }) {
  const enabled = useBetaFeatures((s) => s[feature]);
  if (enabled) return children;
  return <DisabledFeature />;
}

function DisabledFeature() {
  usePageTitle('Feature unavailable');
  return <PageState icon="bi-toggle-off" title="Feature unavailable"
    description="This feature is disabled. Manage optional features in Settings → App → Beta features.">
    <RouteButton to="/settings?tab=app#setting-factory-beta">Open beta settings</RouteButton>
  </PageState>;
}
