import { MultiFileDiff } from '@pierre/diffs/react';
import { DIFF_OPTIONS } from './diffOptions';

// DiffView renders a unified diff between `before` and `after` strings
// using the @pierre/diffs library for syntax highlighting and layout.
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
      options={DIFF_OPTIONS}
      disableWorkerPool
    />
  );
}
