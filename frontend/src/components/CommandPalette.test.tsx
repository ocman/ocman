import { describe, expect, it } from 'vitest';
import type { Project } from '../lib/api';
import {
  absoluteDirectoryQuery,
  buildProjectPaletteResults,
} from './commandPaletteProjectResults';

function project(directory: string, lastUsed: number): Project {
  return {
    directory,
    lastUsed,
    sessionCount: 1,
    messageCount: 0,
    totalTokensIn: 0,
    totalTokensOut: 0,
  };
}

describe('CommandPalette project results', () => {
  it('accepts typed absolute directories as new session targets', () => {
    expect(absoluteDirectoryQuery(' /Users/peter/work/new-app ')).toBe('/Users/peter/work/new-app');
    expect(absoluteDirectoryQuery('relative/path')).toBeNull();
  });

  it('keeps matching known projects before the create-directory result', () => {
    const results = buildProjectPaletteResults([
      project('/Users/peter/workspace/ocman', 10),
      project('/Users/peter/workspace/dotfiles', 20),
    ], '/Users/peter/workspace');

    expect(results.map((r) => r.kind)).toEqual(['project', 'project', 'directory']);
    expect(results[0]).toMatchObject({ kind: 'project', project: { directory: '/Users/peter/workspace/dotfiles' } });
    expect(results[2]).toEqual({ kind: 'directory', directory: '/Users/peter/workspace' });
  });

  it('does not duplicate the create-directory result for an exact known project', () => {
    const results = buildProjectPaletteResults([
      project('/Users/peter/workspace/ocman', 10),
    ], '/Users/peter/workspace/ocman');

    expect(results).toHaveLength(1);
    expect(results[0]).toMatchObject({ kind: 'project', project: { directory: '/Users/peter/workspace/ocman' } });
  });
});
