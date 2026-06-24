import { useCallback, useEffect, useRef, useState } from 'react';

export type SaveState = 'idle' | 'saving' | 'saved' | 'error';

/**
 * useSaveStatus tracks the lifecycle of an async save so a field can show a
 * GitHub-style spinner while in-flight and a checkmark for a few seconds after.
 *
 * Wrap any save promise with `track(() => savePromise)`. The returned `state`
 * drives <SaveStatus>; the "saved" state auto-clears after `savedMs` (5s).
 */
export function useSaveStatus(savedMs = 5000) {
  const [state, setState] = useState<SaveState>('idle');
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => () => { if (timer.current) clearTimeout(timer.current); }, []);

  const track = useCallback(async <T,>(run: () => Promise<T>): Promise<T> => {
    if (timer.current) { clearTimeout(timer.current); timer.current = null; }
    setState('saving');
    try {
      const result = await run();
      setState('saved');
      timer.current = setTimeout(() => setState('idle'), savedMs);
      return result;
    } catch (err) {
      setState('error');
      throw err;
    }
  }, [savedMs]);

  return { state, track };
}
