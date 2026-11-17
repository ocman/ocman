import { useSearchParams } from 'react-router-dom';

export interface UrlSort<K extends string> {
  /** Null keeps the server's recency order. */
  key: K | null;
  desc: boolean;
  /** First click sorts descending (largest first), the next ascending, then back to the default. */
  toggle: (key: K) => void;
}

/** Column sort stored as `sort=cost` (ascending) or `sort=-cost` (descending). */
export function useUrlSort<K extends string>(keys: readonly K[]): UrlSort<K> {
  const [searchParams, setSearchParams] = useSearchParams();
  const raw = searchParams.get('sort') ?? '';
  const desc = raw.startsWith('-');
  const name = (desc ? raw.slice(1) : raw) as K;
  const key = keys.includes(name) ? name : null;
  return {
    key, desc,
    toggle: (next) => setSearchParams((p) => {
      if (key !== next) p.set('sort', `-${next}`);
      else if (desc) p.set('sort', next);
      else p.delete('sort');
      return p;
    }, { replace: true }),
  };
}

/** Stable sort; ties keep the input (recency) order. */
export function sortBy<T, K extends string>(rows: readonly T[], sort: Pick<UrlSort<K>, 'key' | 'desc'>, value: (row: T, key: K) => number): T[] {
  const { key, desc } = sort;
  if (!key) return [...rows];
  return rows.toSorted((a, b) => desc ? value(b, key) - value(a, key) : value(a, key) - value(b, key));
}
