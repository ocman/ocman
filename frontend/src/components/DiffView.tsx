import { MultiFileDiff } from '@pierre/diffs/react';
import { DIFF_OPTIONS } from './diffOptions';

// DiffView renders a unified diff between `before` and `after` strings
// using the @pierre/diffs library for syntax highlighting and layout.
// `plain` skips syntax highlighting (same rows and height) for diffs far
// from the viewport.
export interface DiffViewProps {
  before: string;
  after: string;
  filePath?: string;
  plain?: boolean;
}

export function DiffView({ before, after, filePath, plain = false }: DiffViewProps) {
  const name = filePath || 'file';
  const lang = plain ? 'text' : undefined;
  return (
    <MultiFileDiff
      oldFile={{ name, contents: before, lang }}
      newFile={{ name, contents: after, lang }}
      options={DIFF_OPTIONS}
      disableWorkerPool
    />
  );
}
