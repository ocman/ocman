import { describe, it, expect } from 'vitest';
import { splitPatchSections } from './patchSections';

describe('splitPatchSections', () => {
  it('returns [] for an empty patch', () => {
    expect(splitPatchSections('')).toEqual([]);
  });

  it('keeps a single git-style file diff as one section', () => {
    const diff = [
      'diff --git a/foo.go b/foo.go',
      '--- a/foo.go',
      '+++ b/foo.go',
      '@@ -1,2 +1,3 @@',
      ' line',
      '+added',
      ' line2',
    ].join('\n');
    const sections = splitPatchSections(diff);
    expect(sections).toHaveLength(1);
    expect(sections[0]).toContain('+added');
  });

  it('splits a concatenation of two git-style file diffs', () => {
    const diff = [
      'diff --git a/x.go b/x.go',
      '--- a/x.go',
      '+++ b/x.go',
      '@@ -1,1 +1,2 @@',
      ' a',
      '+b',
      '',
      'diff --git a/x.go b/x.go',
      '--- a/x.go',
      '+++ b/x.go',
      '@@ -2,1 +2,2 @@',
      ' b',
      '+c',
    ].join('\n');
    const sections = splitPatchSections(diff);
    expect(sections).toHaveLength(2);
    expect(sections[0]).toContain('+b');
    expect(sections[0]).not.toContain('+c');
    expect(sections[1]).toContain('+c');
    expect(sections[1]).not.toContain('+b');
  });

  it('splits a concatenation of two plain unified diffs', () => {
    const diff = [
      '--- a/x.go',
      '+++ b/x.go',
      '@@ -1,1 +1,2 @@',
      ' a',
      '+b',
      '',
      '--- a/x.go',
      '+++ b/x.go',
      '@@ -2,1 +2,2 @@',
      ' b',
      '+c',
    ].join('\n');
    const sections = splitPatchSections(diff);
    expect(sections).toHaveLength(2);
    expect(sections[0]).toContain('+b');
    expect(sections[1]).toContain('+c');
  });

  it('falls back to the raw input for a bare hunk with no header', () => {
    const diff = ['@@ -1,2 +1,3 @@', ' line', '+added', ' line2'].join('\n');
    const sections = splitPatchSections(diff);
    expect(sections).toEqual([diff]);
  });
});
