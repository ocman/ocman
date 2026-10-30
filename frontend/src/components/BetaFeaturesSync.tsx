import { useEffect } from 'react';
import { useBetaFeatures } from '../lib/betaFeatures';

/** The backend owns beta opt-ins. Never adopt the retired browser-local flag. */
export function BetaFeaturesSync() {
  useEffect(() => {
    const load = () => { void useBetaFeatures.getState().load(); };
    load();
    const timer = setInterval(load, 10_000);
    window.addEventListener('focus', load);
    return () => { clearInterval(timer); window.removeEventListener('focus', load); };
  }, []);
  return null;
}
