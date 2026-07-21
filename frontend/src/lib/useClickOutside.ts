import { useEffect, useEffectEvent, type RefObject } from 'react';

export function useClickOutside(
  ref: RefObject<HTMLElement | null>,
  enabled: boolean,
  onOutside: () => void,
) {
  const handleOutside = useEffectEvent(onOutside);

  useEffect(() => {
    if (!enabled) return;
    const handle = (event: MouseEvent) => {
      if (ref.current && !ref.current.contains(event.target as Node)) handleOutside();
    };
    document.addEventListener('mousedown', handle);
    return () => document.removeEventListener('mousedown', handle);
  }, [enabled, ref]);
}
