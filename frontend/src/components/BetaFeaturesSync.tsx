import { useEffect } from 'react';
import { useBetaFeatures } from '../lib/betaFeatures';
import { startVisiblePolling } from '../lib/visiblePolling';

/** The backend owns beta opt-ins. Never adopt the retired browser-local flag. */
export function BetaFeaturesSync() {
  useEffect(() => {
    return startVisiblePolling(() => useBetaFeatures.getState().load(), 10_000);
  }, []);
  return null;
}
