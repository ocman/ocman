import { PatchDiff } from '@pierre/diffs/react';
import { DIFF_OPTIONS } from './diffOptions';

// RawDiffView renders a unified-diff string (the body of a single
// `diff --git ...` section, as produced by `git diff`) using the
// @pierre/diffs library for syntax highlighting and layout.

export interface RawDiffViewProps {
  // Unified-diff body (one file's `diff --git ...` section).
  diff: string;
  // File path used to infer syntax-highlight language. Unused by
  // @pierre/diffs directly (it infers from the patch header), but
  // kept in the interface for compatibility with existing call sites.
  filePath?: string;
}

export function RawDiffView({ diff }: RawDiffViewProps) {
  return (
    <PatchDiff
      patch={diff}
      options={DIFF_OPTIONS}
      disableWorkerPool
    />
  );
}
