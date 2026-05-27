import { MultiFileDiff } from '@pierre/diffs/react';

// DiffView renders a unified diff between `before` and `after` strings
// using the @pierre/diffs library for syntax highlighting and layout.
// The WorkerPoolContextProvider must be mounted above this component
// in the tree (added to App.tsx).
export interface DiffViewProps {
  before: string;
  after: string;
  filePath?: string;
  startLine?: number;
}

export function DiffView({ before, after, filePath }: DiffViewProps) {
  const name = filePath || 'file';
  return (
    <MultiFileDiff
      oldFile={{ name, contents: before }}
      newFile={{ name, contents: after }}
      options={{ theme: 'github-dark-dimmed', diffStyle: 'unified', disableFileHeader: true, overflow: 'wrap' }}
      disableWorkerPool
    />
  );
}
