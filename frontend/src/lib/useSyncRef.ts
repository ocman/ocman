import { useEffect, useRef } from 'react';
import type { MutableRefObject } from 'react';

/**
 * Returns a ref whose `.current` always tracks the latest `value`.
 *
 * Replaces the boilerplate pattern:
 *
 * ```ts
 * const fooRef = useRef(foo);
 * useEffect(() => { fooRef.current = foo; }, [foo]);
 * ```
 *
 * Useful for keeping the freshest value accessible inside long-lived
 * effects (event listeners, EventSource handlers, intervals) without
 * adding the value to the effect's dependency array.
 */
export function useSyncRef<T>(value: T): MutableRefObject<T> {
  const ref = useRef(value);
  useEffect(() => {
    ref.current = value;
  }, [value]);
  return ref;
}
