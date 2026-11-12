import { deployedBuildHash } from './buildUpdate';
import { canAutomaticallyReload } from './automaticReload';
import { BUILD_HASH, claimBuildReload } from './buildReloadBudget';

/** Recover verified version skew, not arbitrary network or same-build errors. */
export function installPreloadRecovery(buildHash: string, reload = () => window.location.reload()) {
  let request: AbortController | undefined;
  let stopped = false;
  let reloading = false;
  const recover = () => {
    if (!BUILD_HASH.test(buildHash) || request || reloading || !canAutomaticallyReload()) return;
    const controller = new AbortController();
    request = controller;
    const timeout = setTimeout(() => controller.abort(), 10_000);
    void deployedBuildHash(controller.signal).then(hash => {
      // The user may have typed or attached a file while the probe was pending.
      if (stopped || !hash || hash === buildHash || !canAutomaticallyReload() || !claimBuildReload(buildHash, hash)) return;
      reloading = true;
      reload();
    }).finally(() => { clearTimeout(timeout); request = undefined; });
  };
  // Do not preventDefault: Vite's lazy import still needs to reject into the
  // ErrorBoundary when recovery is unsafe, unavailable or the reload is canceled.
  window.addEventListener('vite:preloadError', recover);
  return () => {
    stopped = true;
    request?.abort();
    window.removeEventListener('vite:preloadError', recover);
  };
}
