import { useEffect, useState } from 'react';
import { readStorage, writeStorage } from './storage';

export function useSessionFilterPreference(name: string, defaultValue: boolean) {
  const key = `ocman:sidebar-filter:${name}`;
  const [value, setValue] = useState(() => {
    const stored = readStorage(key);
    return stored === 'true' ? true : stored === 'false' ? false : defaultValue;
  });
  // Storage may be blocked or full; filters still work in memory.
  useEffect(() => { writeStorage(key, String(value)); }, [key, value]);
  return [value, setValue] as const;
}
