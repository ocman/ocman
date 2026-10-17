import { useSyncExternalStore } from 'react';

// Keep in sync with the phone-only styles in PhoneNavigation.css and PhoneTables.css.
const PHONE_QUERY = '(max-width: 640px)';

function subscribe(onChange: () => void) {
  const media = window.matchMedia?.(PHONE_QUERY);
  media?.addEventListener('change', onChange);
  return () => media?.removeEventListener('change', onChange);
}

export function useIsPhone() {
  return useSyncExternalStore(subscribe, () => !!window.matchMedia?.(PHONE_QUERY).matches, () => false);
}
