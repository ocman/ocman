import { describe, it, expect } from 'vitest';
import { parseGitHubUrl } from './githubPreview';

describe('parseGitHubUrl', () => {
  it('parses a PR URL', () => {
    const ref = parseGitHubUrl('https://github.com/aspect-analytics/weave-agent/pull/15');
    expect(ref).toEqual({ kind: 'pr', owner: 'aspect-analytics', repo: 'weave-agent', number: 15 });
  });

  it('parses a PR URL with trailing path', () => {
    const ref = parseGitHubUrl('https://github.com/owner/repo/pull/42/files');
    expect(ref).toEqual({ kind: 'pr', owner: 'owner', repo: 'repo', number: 42 });
  });

  it('parses an issue URL', () => {
    const ref = parseGitHubUrl('https://github.com/owner/repo/issues/7');
    expect(ref).toEqual({ kind: 'issue', owner: 'owner', repo: 'repo', number: 7 });
  });

  it('parses a commit URL (full sha)', () => {
    const ref = parseGitHubUrl('https://github.com/owner/repo/commit/abc1234def5678901234567890123456789abcde');
    expect(ref).toEqual({
      kind: 'commit',
      owner: 'owner',
      repo: 'repo',
      sha: 'abc1234def5678901234567890123456789abcde',
    });
  });

  it('parses a commit URL (short sha)', () => {
    const ref = parseGitHubUrl('https://github.com/owner/repo/commit/abc1234');
    expect(ref).toEqual({ kind: 'commit', owner: 'owner', repo: 'repo', sha: 'abc1234' });
  });

  it('returns null for non-GitHub URLs', () => {
    expect(parseGitHubUrl('https://example.com/foo/bar')).toBeNull();
    expect(parseGitHubUrl('https://gitlab.com/owner/repo/merge_requests/1')).toBeNull();
  });

  it('returns null for bare GitHub URLs', () => {
    expect(parseGitHubUrl('https://github.com/owner/repo')).toBeNull();
    expect(parseGitHubUrl('https://github.com/owner/repo/tree/main')).toBeNull();
  });

  it('returns null for empty string', () => {
    expect(parseGitHubUrl('')).toBeNull();
  });
});
