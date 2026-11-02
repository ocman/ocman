// apply_patch envelopes (`*** Begin Patch` / `*** Update File: <path>`):
// per-file sections, synthetic unified diffs and tool-card summaries.

/** Single section in a parsed `*** … File:` patch payload. */
export interface PatchSection {
  action: 'add' | 'update' | 'delete';
  path: string;
}

export interface ApplyPatchFileDiff {
  action: 'add' | 'update' | 'delete' | 'rename';
  path: string;
  oldPath?: string;
  patch: string;
}

/**
 * Walk a `*** … File: <path>` patch and return one entry per file
 * touched. Order matches the order of the corresponding header lines.
 */
export function parsePatchSections(patchText: string): PatchSection[] {
  return patchText.split('\n').flatMap<PatchSection>((line) => {
    const updateMatch = line.match(/^\*\*\* Update File: (.+)$/);
    if (updateMatch) return [{ action: 'update' as const, path: updateMatch[1] }];
    const addMatch = line.match(/^\*\*\* Add File: (.+)$/);
    if (addMatch) return [{ action: 'add' as const, path: addMatch[1] }];
    const deleteMatch = line.match(/^\*\*\* Delete File: (.+)$/);
    if (deleteMatch) return [{ action: 'delete' as const, path: deleteMatch[1] }];
    return [];
  });
}

interface PatchSectionWithLines {
  action: ApplyPatchFileDiff['action'];
  lines: string[];
  oldPath?: string;
  path: string;
}

function parsePatchSectionsWithLines(patchText: string): PatchSectionWithLines[] {
  const sections: PatchSectionWithLines[] = [];
  let current: PatchSectionWithLines | null = null;

  const pushCurrent = () => {
    if (current) sections.push(current);
  };

  for (const line of patchText.split('\n')) {
    const markerLine = line.trimStart();

    if (markerLine === '*** Begin Patch') continue;
    if (markerLine === '*** End Patch') {
      pushCurrent();
      current = null;
      continue;
    }

    const headerMatch = markerLine.match(/^\*\*\* (Add|Update|Delete) File: (.+)$/);
    if (headerMatch) {
      pushCurrent();
      const action = headerMatch[1] === 'Add'
        ? 'add'
        : headerMatch[1] === 'Delete'
          ? 'delete'
          : 'update';
      current = { action, path: headerMatch[2], lines: [] };
      continue;
    }

    if (!current) continue;
    const moveMatch = markerLine.match(/^\*\*\* Move to: (.+)$/);
    if (moveMatch) {
      current.oldPath = current.path;
      current.action = 'rename';
      current.path = moveMatch[1];
      continue;
    }
    current.lines.push(line);
  }

  pushCurrent();
  return sections;
}

function unifiedRange(start: number, count: number): string {
  if (count === 0) return `${start},0`;
  if (count === 1) return `${start}`;
  return `${start},${count}`;
}

/**
 * Convert apply_patch's envelope format into a synthetic unified diff.
 * The patch format only contains changed hunks, not full file contents,
 * so line numbers are snippet-relative; that is enough for the diff
 * renderer to display clean multi-file sections without raw markers.
 */
export function applyPatchToUnifiedFileDiffs(patchText: string): ApplyPatchFileDiff[] {
  const sections = parsePatchSectionsWithLines(patchText);
  if (sections.length === 0) return [];

  return sections.map((section) => {
    let oldCount = 0;
    let newCount = 0;
    const hunkLines: string[] = [];

    for (const rawLine of section.lines) {
      if (rawLine.startsWith('@@')) continue;
      if (rawLine.startsWith('\\ No newline at end of file')) {
        hunkLines.push(rawLine);
        continue;
      }

      if (rawLine.startsWith('+')) {
        newCount += 1;
        hunkLines.push(rawLine);
      } else if (rawLine.startsWith('-')) {
        oldCount += 1;
        hunkLines.push(rawLine);
      } else {
        oldCount += 1;
        newCount += 1;
        hunkLines.push(rawLine.startsWith(' ') ? rawLine : ` ${rawLine}`);
      }
    }

    const diffOldPath = section.oldPath ?? section.path;
    const oldPath = section.action === 'add' ? '/dev/null' : `a/${diffOldPath}`;
    const newPath = section.action === 'delete' ? '/dev/null' : `b/${section.path}`;
    const oldStart = oldCount === 0 ? 0 : 1;
    const newStart = newCount === 0 ? 0 : 1;

    const patch = [
      `diff --git a/${diffOldPath} b/${section.path}`,
      `--- ${oldPath}`,
      `+++ ${newPath}`,
      `@@ -${unifiedRange(oldStart, oldCount)} +${unifiedRange(newStart, newCount)} @@`,
      ...hunkLines,
    ].join('\n');

    return {
      action: section.action,
      path: section.path,
      oldPath: section.oldPath,
      patch,
    };
  });
}

/**
 * Trim absolute paths down to a project-relative form so they fit on
 * a single tool-card line. Recognised prefixes (`/frontend/`,
 * `/internal/`, `/docs/`, `/.github/`) keep everything from the
 * marker onward; anything else falls back to the trailing two
 * segments. Relative paths pass through unchanged.
 */
export function shortenPatchPath(path: string): string {
  if (!path.startsWith('/')) return path;

  const markers = ['/frontend/', '/internal/', '/docs/', '/.github/'];
  for (const marker of markers) {
    const index = path.indexOf(marker);
    if (index >= 0) return path.slice(index + 1);
  }

  const parts = path.split('/').filter(Boolean);
  return parts.slice(-2).join('/');
}

/**
 * One-line patch summary for the tool-card title. A patch touching a
 * single file shows the action verb and shortened path; multi-file
 * patches show the file count and a per-action breakdown.
 */
export function summarizePatch(patchText: string): string {
  const sections = parsePatchSections(patchText);
  if (sections.length === 0) return 'Patch';
  if (sections.length === 1) {
    const section = sections[0];
    const action = section.action === 'add' ? 'Add' : section.action === 'delete' ? 'Delete' : 'Update';
    return `${action} ${shortenPatchPath(section.path)}`;
  }

  const counts = sections.reduce<{ add: number; update: number; delete: number }>(
    (acc, section) => {
      acc[section.action] += 1;
      return acc;
    },
    { add: 0, update: 0, delete: 0 },
  );
  const parts = [
    counts.add ? `${counts.add} add${counts.add === 1 ? '' : 's'}` : null,
    counts.update ? `${counts.update} update${counts.update === 1 ? '' : 's'}` : null,
    counts.delete ? `${counts.delete} delete${counts.delete === 1 ? '' : 's'}` : null,
  ].filter(Boolean);
  return `Patch ${sections.length} files${parts.length ? ` (${parts.join(', ')})` : ''}`;
}
