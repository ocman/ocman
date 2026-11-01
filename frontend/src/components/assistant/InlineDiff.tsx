// @pierre/diffs and its Shiki highlighter are heavy; LazyInlineDiff loads
// this module only when a thread renders an edit diff.
import { Suspense } from 'react';
import { MultiFileDiff } from '@pierre/diffs/react';
import { DIFF_OPTIONS } from '../diffOptions';
import type { DiffPayload } from './toolOutputFormat';

// Renders a before/after diff using @pierre/diffs. `plain` skips syntax
// highlighting (same rows and height) for diffs far from the viewport.
export function InlineDiff({ payload, plain = false }: { payload: DiffPayload; plain?: boolean }) {
  const name = payload.filePath || 'file';
  const lang = plain ? 'text' : undefined;
  return (
    <Suspense fallback={null}>
      <MultiFileDiff
        oldFile={{ name, contents: payload.before, lang }}
        newFile={{ name, contents: payload.after, lang }}
        options={DIFF_OPTIONS}
        disableWorkerPool
      />
    </Suspense>
  );
}
