import { PatchDiff } from '@pierre/diffs/react';
import { DIFF_OPTIONS } from './diffOptions';
import { splitPatchSections } from '../lib/patchSections';

// RawDiffView renders a unified-diff string using the @pierre/diffs
// library for syntax highlighting and layout.
//
// `@pierre/diffs`' PatchDiff component requires the patch to describe
// *exactly one* file diff — it throws "FileDiff: Provided patch must
// contain exactly 1 file diff" otherwise. A single working-tree
// `git diff` section satisfies that, but a session FileChange.Patch
// is the *concatenation* of every edit's patch (see
// internal/platforms/opencode/changes.go), so a file touched by more
// than one edit produces a patch with multiple file-diff sections.
//
// To stay robust we split the incoming patch into its individual
// file-diff sections and render one PatchDiff per section. A
// well-formed single-file patch yields exactly one section, so the
// common case is unchanged.

export interface RawDiffViewProps {
  // Unified-diff body. Usually one file's `diff --git ...` section,
  // but may be several sections concatenated (multi-edit files).
  diff: string;
  // File path used to infer syntax-highlight language. Unused by
  // @pierre/diffs directly (it infers from the patch header), but
  // kept in the interface for compatibility with existing call sites.
  filePath?: string;
}

export function RawDiffView({ diff }: RawDiffViewProps) {
  const sections = splitPatchSections(diff);

  if (sections.length <= 1) {
    return (
      <PatchDiff
        patch={sections[0] ?? diff}
        options={DIFF_OPTIONS}
        disableWorkerPool
      />
    );
  }

  return (
    <div className="oc-raw-diff-sections">
      {sections.map((section, index) => (
        <PatchDiff
          key={index}
          patch={section}
          options={DIFF_OPTIONS}
          disableWorkerPool
        />
      ))}
    </div>
  );
}
