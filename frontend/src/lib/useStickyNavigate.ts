// useStickyNavigate
//
// A thin wrapper around react-router's useNavigate() that preserves
// a configured allow-list of query params across navigations.
//
// MOTIVATION
//
// Diagnostic flags (`?debug`) need to survive when the user clicks
// from one session to another. The default `navigate('/session/X')`
// call drops the search string, so the diagnostic logging silently
// turns off after the first click.
//
// USAGE
//
//   const navigate = useStickyNavigate();   // defaults to ['debug']
//   navigate(`/session/${id}`);             // becomes /session/X?debug
//
//   // or with custom allow-list
//   const navigate = useStickyNavigate(['debug', 'profile']);
//
// SHAPE
//
// The returned function mirrors react-router's overload:
//
//   navigate(to: string, options?)         — string path
//   navigate(delta: number)                — history delta (passed through)
//
// Options are forwarded as-is. If the caller's `to` already contains
// a `?` query string, sticky params NOT already present in `to` are
// merged in; existing keys in `to` win (caller intent is respected).
//
// Hash fragments are preserved as-is — sticky params attach before
// the hash.

import { useCallback } from 'react';
import { useLocation, useNavigate, type NavigateOptions } from 'react-router-dom';

const DEFAULT_STICKY_PARAMS = ['debug'];

export type StickyNavigate = {
  (to: string, options?: NavigateOptions): void;
  (delta: number): void;
};

export function useStickyNavigate(stickyParams: string[] = DEFAULT_STICKY_PARAMS): StickyNavigate {
  const navigate = useNavigate();
  const location = useLocation();

  return useCallback(((to: string | number, options?: NavigateOptions) => {
    if (typeof to !== 'string') {
      navigate(to);
      return;
    }
    navigate(applyStickyParams(to, location.search, stickyParams), options);
    // location.search is the only thing the closure depends on that
    // can change between calls; the sticky list is captured by
    // reference and the caller is expected to pass a stable array.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }) as StickyNavigate, [navigate, location.search, stickyParams.join(',')]);
}

/**
 * Merge sticky query params from `currentSearch` into `to` and
 * return the resulting URL. Exported for unit testing — the hook
 * itself is just a tiny wrapper around this pure function.
 *
 * Behaviour:
 *
 *   - `to` is treated as a relative or absolute path with optional
 *     `?query` and `#hash`.
 *   - Sticky params already present on `to` are NOT overwritten —
 *     the caller's explicit value wins.
 *   - Sticky params absent from `to` but present (with any value,
 *     including empty) in `currentSearch` are appended.
 *   - Sticky params absent from both are skipped.
 *   - The hash fragment from `to` (if any) is preserved at the end.
 *   - When no sticky params end up applying, returns `to` unchanged.
 */
export function applyStickyParams(
  to: string,
  currentSearch: string,
  stickyParams: readonly string[],
): string {
  if (stickyParams.length === 0) return to;
  // Cheap fast path — if there's nothing to inherit, return as-is.
  const current = new URLSearchParams(currentSearch);
  const inherited: Array<[string, string]> = [];
  for (const key of stickyParams) {
    if (current.has(key)) {
      inherited.push([key, current.get(key) ?? '']);
    }
  }
  if (inherited.length === 0) return to;

  // Split `to` into [path, query, hash]. URL parsing without a base
  // is brittle on relative paths, so do it manually.
  let path = to;
  let query = '';
  let hash = '';
  const hashIdx = path.indexOf('#');
  if (hashIdx >= 0) {
    hash = path.slice(hashIdx);
    path = path.slice(0, hashIdx);
  }
  const queryIdx = path.indexOf('?');
  if (queryIdx >= 0) {
    query = path.slice(queryIdx + 1);
    path = path.slice(0, queryIdx);
  }

  const out = new URLSearchParams(query);
  for (const [k, v] of inherited) {
    if (!out.has(k)) out.set(k, v);
  }
  const queryString = out.toString();
  return queryString ? `${path}?${queryString}${hash}` : `${path}${hash}`;
}
