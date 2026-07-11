import { useEffect } from 'react';

/**
 * Forward a sidebar's refresh callback and loading state up to
 * RightPanel. Extracted from SessionChangesSidebar / SessionInfoSidebar
 * / WorkingTreeChangesSidebar which shared these two effects verbatim.
 */
export function useSidebarCallbacks(opts: {
  refresh: () => void;
  loading: boolean;
  onRefresh?: (refresh: () => void) => void;
  onLoadingChange?: (loading: boolean) => void;
}) {
  const { refresh, loading, onRefresh, onLoadingChange } = opts;
  useEffect(() => {
    if (!onRefresh) return;
    onRefresh(refresh);
  }, [refresh, onRefresh]);
  useEffect(() => {
    if (!onLoadingChange) return;
    onLoadingChange(loading);
  }, [loading, onLoadingChange]);
}
