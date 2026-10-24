import { useLocation, useNavigate } from 'react-router-dom';
import { useIsPhone } from './useIsPhone';

type Overlay = { name: string; value: string; scope: string };

/** A same-URL entry lets the browser's own back gesture dismiss phone overlays.
 * No touch handler competes with Safari or Android's system navigation.
 */
export function usePhoneOverlay<T extends string>(name: string) {
  const location = useLocation();
  const navigate = useNavigate();
  const phone = useIsPhone();
  const scope = location.pathname + location.search + location.hash;
  const state = location.state as { phoneOverlay?: Overlay } | null;
  const entry = state?.phoneOverlay;
  const value = phone && entry?.name === name && entry.scope === scope ? entry.value as T : null;
  const setValue = (next: T | null) => {
    if (!phone) return;
    if (next === null) {
      // Only our own entry can be popped. Direct links never navigate off-site.
      if (value !== null) void navigate(-1);
    } else if (next !== value) {
      void navigate({ pathname: location.pathname, search: location.search, hash: location.hash }, {
        state: { ...state, phoneOverlay: { name, value: next, scope } },
        replace: value !== null,
        preventScrollReset: true,
      });
    }
  };
  return [value, setValue] as const;
}
