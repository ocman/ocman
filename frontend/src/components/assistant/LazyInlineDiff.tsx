import { useRef } from 'react';
import { useIsPrinting } from '../../lib/useIsPrinting';
import { useNearViewport } from '../../lib/useNearViewport';
import { lazyComponent } from '../lazyComponent';
import type { DiffPayload } from './toolOutputFormat';

// @pierre/diffs and its Shiki highlighter are heavy; load them with the first diff.
const DiffView = lazyComponent(() => import('../DiffView').then((m) => m.DiffView), 'Loading diff…');

/**
 * An inline edit diff that skips syntax highlighting until it scrolls near
 * the thread viewport. Highlighting every diff synchronously on mount was
 * the largest cost of opening a long session; the plain rendering has the
 * same rows, so nothing shifts when colour arrives. The diff renderer
 * itself loads with the first diff.
 */
export function LazyInlineDiff({ payload }: { payload: DiffPayload }) {
  const ref = useRef<HTMLDivElement>(null);
  const near = useNearViewport(ref, '.oc-thread-viewport');
  const printing = useIsPrinting();
  return (
    <div ref={ref} className="oc-tool-output">
      <DiffView before={payload.before} after={payload.after} filePath={payload.filePath} plain={!near && !printing} />
    </div>
  );
}
