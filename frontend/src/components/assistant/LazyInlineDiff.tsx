import { useRef } from 'react';
import { useIsPrinting } from '../../lib/useIsPrinting';
import { useNearViewport } from '../../lib/useNearViewport';
import { InlineDiff } from './toolRenderers';
import type { DiffPayload } from './toolOutputFormat';

/**
 * An inline edit diff that skips syntax highlighting until it scrolls near
 * the thread viewport. Highlighting every diff synchronously on mount was
 * the largest cost of opening a long session; the plain rendering has the
 * same rows, so nothing shifts when colour arrives.
 */
export function LazyInlineDiff({ payload }: { payload: DiffPayload }) {
  const ref = useRef<HTMLDivElement>(null);
  const near = useNearViewport(ref);
  const printing = useIsPrinting();
  return (
    <div ref={ref} className="oc-tool-output">
      <InlineDiff payload={payload} plain={!near && !printing} />
    </div>
  );
}
