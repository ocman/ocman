import { lazy, Suspense, type ComponentType } from 'react';
import { LoadingState } from './LoadingState';

/**
 * A component whose module (and its heavy dependencies: charts, graph
 * layout, the terminal) loads on first render, behind a LoadingState.
 * `load` resolves the component, e.g. `() => import('./X').then((m) => m.X)`.
 */
export function lazyComponent<P extends object>(load: () => Promise<ComponentType<P>>, label = 'Loading…') {
  const Lazy = lazy(async () => ({ default: await load() }));
  return function LazyComponent(props: P) {
    return <Suspense fallback={<LoadingState>{label}</LoadingState>}><Lazy {...props} /></Suspense>;
  };
}
