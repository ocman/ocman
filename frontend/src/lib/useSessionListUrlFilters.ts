import { useSearchParams } from 'react-router-dom';
import { useUiStore } from './uiStore';

/**
 * Time window and archived visibility shared by every session list, stored in
 * the URL (`t` hours, 0 = all time; `a=1` shows archived). Both lists default
 * to the user's configured window and hide archived sessions.
 */
export function useSessionListUrlFilters() {
  const [searchParams, setSearchParams] = useSearchParams();
  const defaultHours = useUiStore((s) => s.dashboardTimeRangeDefault);
  const t = searchParams.get('t');
  const parsed = t === null ? NaN : parseInt(t, 10);
  const timeRange = Number.isFinite(parsed) && parsed >= 0 ? parsed : defaultHours;
  const showArchived = searchParams.get('a') === '1';
  return {
    timeRange,
    /** Undefined means no lower bound ("All"). */
    sinceHours: timeRange > 0 ? timeRange : undefined,
    setTimeRange: (v: number) => setSearchParams((p) => { p.set('t', String(v)); return p; }, { replace: true }),
    showArchived,
    setShowArchived: (v: boolean) => setSearchParams((p) => {
      if (v) p.set('a', '1'); else p.delete('a');
      return p;
    }, { replace: true }),
  };
}
