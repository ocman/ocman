import { describe, it, expect } from 'vitest';
import {
  parsePatchSections,
  applyPatchToUnifiedFileDiffs,
  shortenPatchPath,
  summarizePatch,
} from './applyPatch';

describe('parsePatchSections', () => {
  it('parses Add / Update / Delete sections', () => {
    const patch = [
      '*** Begin Patch',
      '*** Add File: a.txt',
      '*** Update File: /repo/b.go',
      '*** Delete File: c.md',
      '*** End Patch',
    ].join('\n');
    expect(parsePatchSections(patch)).toEqual([
      { action: 'add', path: 'a.txt' },
      { action: 'update', path: '/repo/b.go' },
      { action: 'delete', path: 'c.md' },
    ]);
  });

  it('returns an empty list when no headers are present', () => {
    expect(parsePatchSections('no patch here')).toEqual([]);
  });
});

describe('shortenPatchPath', () => {
  it('returns relative paths unchanged', () => {
    expect(shortenPatchPath('relative/path.ts')).toBe('relative/path.ts');
    expect(shortenPatchPath('a.txt')).toBe('a.txt');
  });

  it('keeps the marker prefix when the path matches a known root', () => {
    expect(shortenPatchPath('/Users/me/project/frontend/src/x.ts'))
      .toBe('frontend/src/x.ts');
    expect(shortenPatchPath('/abs/repo/internal/server/foo.go'))
      .toBe('internal/server/foo.go');
    expect(shortenPatchPath('/etc/.github/workflows/ci.yml'))
      .toBe('.github/workflows/ci.yml');
  });

  it('falls back to the trailing two segments for unknown roots', () => {
    expect(shortenPatchPath('/var/log/app/api.log')).toBe('app/api.log');
  });
});

describe('summarizePatch', () => {
  it('returns "Patch" when no sections parse', () => {
    expect(summarizePatch('no sections')).toBe('Patch');
  });

  it('returns the action verb + path for a single section', () => {
    expect(summarizePatch('*** Update File: /repo/internal/foo.go'))
      .toBe('Update internal/foo.go');
    expect(summarizePatch('*** Add File: a.txt')).toBe('Add a.txt');
    expect(summarizePatch('*** Delete File: x')).toBe('Delete x');
  });

  it('counts files for multi-section patches', () => {
    const patch = [
      '*** Add File: a',
      '*** Add File: b',
      '*** Update File: c',
      '*** Delete File: d',
    ].join('\n');
    expect(summarizePatch(patch))
      .toBe('Patch 4 files (2 adds, 1 update, 1 delete)');
  });

  it('uses singular form for count of one in multi-section patches', () => {
    const patch = [
      '*** Add File: a',
      '*** Update File: b',
    ].join('\n');
    // 2 sections -> multi-section path
    expect(summarizePatch(patch)).toBe('Patch 2 files (1 add, 1 update)');
  });
});

describe('applyPatchToUnifiedFileDiffs', () => {
  const unifiedDiff = (patch: string) => applyPatchToUnifiedFileDiffs(patch).map((file) => file.patch).join('\n');

  it('preserves per-file actions for patch rendering', () => {
    const patch = [
      '*** Begin Patch',
      '*** Update File: a.txt',
      '-old',
      '+new',
      '*** Delete File: b.txt',
      '-gone',
      '*** End Patch',
    ].join('\n');

    expect(applyPatchToUnifiedFileDiffs(patch).map((file) => ({
      action: file.action,
      path: file.path,
    }))).toEqual([
      { action: 'update', path: 'a.txt' },
      { action: 'delete', path: 'b.txt' },
    ]);
  });

  it('converts multi-file apply_patch payloads into unified diff sections', () => {
    const patch = [
      '*** Begin Patch',
      '*** Update File: internal/a.go',
      '@@',
      ' func old() {',
      '-\treturn 1',
      '+\treturn 2',
      ' }',
      '*** Add File: docs/new.md',
      '+# New',
      '+content',
      '*** End Patch',
    ].join('\n');

    expect(unifiedDiff(patch)).toBe([
      'diff --git a/internal/a.go b/internal/a.go',
      '--- a/internal/a.go',
      '+++ b/internal/a.go',
      '@@ -1,3 +1,3 @@',
      ' func old() {',
      '-\treturn 1',
      '+\treturn 2',
      ' }',
      'diff --git a/docs/new.md b/docs/new.md',
      '--- /dev/null',
      '+++ b/docs/new.md',
      '@@ -0,0 +1,2 @@',
      '+# New',
      '+content',
    ].join('\n'));
  });

  it('returns null when no apply_patch sections are present', () => {
    expect(applyPatchToUnifiedFileDiffs('no patch here')).toEqual([]);
  });

  it('does not add phantom context after the end marker', () => {
    const patch = [
      '*** Begin Patch',
      '*** Add File: a.txt',
      '+one',
      '*** End Patch',
      '',
    ].join('\n');

    expect(unifiedDiff(patch)).toBe([
      'diff --git a/a.txt b/a.txt',
      '--- /dev/null',
      '+++ b/a.txt',
      '@@ -0,0 +1 @@',
      '+one',
    ].join('\n'));
  });

  it('accepts indented apply_patch markers', () => {
    const patch = [
      '  *** Begin Patch',
      '  *** Update File: internal/a.go',
      '@@',
      ' value',
      '-old',
      '+new',
      '  *** End Patch',
    ].join('\n');

    expect(unifiedDiff(patch)).toBe([
      'diff --git a/internal/a.go b/internal/a.go',
      '--- a/internal/a.go',
      '+++ b/internal/a.go',
      '@@ -1,2 +1,2 @@',
      ' value',
      '-old',
      '+new',
    ].join('\n'));
  });

  it('marks moved files as renames', () => {
    const patch = [
      '*** Begin Patch',
      '*** Update File: old.txt',
      '*** Move to: new.txt',
      '-old',
      '+new',
      '*** End Patch',
    ].join('\n');

    expect(applyPatchToUnifiedFileDiffs(patch)[0]).toMatchObject({
      action: 'rename',
      oldPath: 'old.txt',
      path: 'new.txt',
      patch: [
        'diff --git a/old.txt b/new.txt',
        '--- a/old.txt',
        '+++ b/new.txt',
        '@@ -1 +1 @@',
        '-old',
        '+new',
      ].join('\n'),
    });
  });
});
