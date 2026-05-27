import { PatchDiff } from '@pierre/diffs/react';

// RawDiffView renders a unified-diff string (the body of a single
// `diff --git ...` section, as produced by `git diff`) using the
// @pierre/diffs library for syntax highlighting and layout.
// The WorkerPoolContextProvider must be mounted above this component
// in the tree (added to App.tsx).

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
      options={{ theme: 'github-dark-dimmed', diffStyle: 'unified', disableFileHeader: true, overflow: 'wrap' }}
      disableWorkerPool
    />
  );
}
