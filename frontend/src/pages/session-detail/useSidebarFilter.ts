import { useEffect, useState } from 'react';

export function useSidebarFilter(name: string, defaultValue: boolean) {
  const key = `ocman:sidebar-filter:${name}`;
  const [value, setValue] = useState(() => {
    try {
      const stored = localStorage.getItem(key);
      return stored === 'true' ? true : stored === 'false' ? false : defaultValue;
    } catch {
      return defaultValue;
    }
  });
  useEffect(() => {
    try {
      localStorage.setItem(key, String(value));
    } catch {
      // Storage may be blocked or full; filters still work in memory.
    }
  }, [key, value]);
  return [value, setValue] as const;
}
