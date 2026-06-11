// splitPatchSections breaks a (possibly multi-file) unified diff into
// its individual file-diff sections, mirroring the boundary detection
// @pierre/diffs uses internally: `diff --git` lines for git-style
// patches, or `--- <file>` header lines for plain unified diffs.
//
// This matters because `@pierre/diffs`' PatchDiff component requires
// the patch to describe *exactly one* file diff — it throws
// "FileDiff: Provided patch must contain exactly 1 file diff"
// otherwise. A session FileChange.Patch is the concatenation of every
// edit's patch (see internal/platforms/opencode/changes.go), so a
// file touched by more than one edit yields multiple file-diff
// sections that must be rendered separately.
//
// When no boundary is found the whole input is returned as a single
// section so callers always get something to render (PatchDiff still
// handles a lone `@@` hunk).
export function splitPatchSections(diff: string): string[] {
  if (!diff) return [];

  const isGit = diff.startsWith('diff --git') || diff.includes('\ndiff --git');
  const boundary = isGit ? /^diff --git /m : /^---\s+\S/m;

  const lines = diff.split('\n');
  const sections: string[] = [];
  let current: string[] = [];

  for (const line of lines) {
    const startsSection = isGit
      ? line.startsWith('diff --git ')
      : /^---\s+\S/.test(line);
    if (startsSection && current.length > 0) {
      sections.push(current.join('\n'));
      current = [];
    }
    current.push(line);
  }
  if (current.length > 0) sections.push(current.join('\n'));

  // Drop any leading blob before the first boundary (e.g. a blank
  // separator line inserted between concatenated edits) so each
  // section starts at a real file-diff header.
  const cleaned = sections
    .map((s) => s.trim())
    .filter((s) => s.length > 0 && boundary.test(s));

  // Fall back to the raw input when we couldn't identify any
  // file-diff section (e.g. a bare hunk with no header).
  return cleaned.length > 0 ? cleaned : [diff];
}
