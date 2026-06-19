import { describe, it, expect } from 'vitest';
import { parseGitHubUrl, parseForgejoUrl, extractForgejoUrls } from './githubPreview';

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

describe('parseForgejoUrl', () => {
  const hosts = ['code.example.com', 'codeberg.org'];

  it('parses a PR URL (plural /pulls/)', () => {
    const ref = parseForgejoUrl('https://code.example.com/alice/myproj/pulls/7', hosts);
    expect(ref).toEqual({ kind: 'pr', owner: 'alice', repo: 'myproj', number: 7 });
  });

  it('parses an issue URL', () => {
    const ref = parseForgejoUrl('https://codeberg.org/owner/repo/issues/12', hosts);
    expect(ref).toEqual({ kind: 'issue', owner: 'owner', repo: 'repo', number: 12 });
  });

  it('parses a commit URL', () => {
    const ref = parseForgejoUrl('https://code.example.com/owner/repo/commit/abc1234', hosts);
    expect(ref).toEqual({ kind: 'commit', owner: 'owner', repo: 'repo', sha: 'abc1234' });
  });

  it('returns null for a host not in the list', () => {
    expect(parseForgejoUrl('https://other.example.org/owner/repo/pulls/1', hosts)).toBeNull();
  });

  it('returns null when no hosts are configured', () => {
    expect(parseForgejoUrl('https://code.example.com/owner/repo/pulls/1', [])).toBeNull();
  });

  it('returns null for GitHub-style singular /pull/ path', () => {
    expect(parseForgejoUrl('https://code.example.com/owner/repo/pull/1', hosts)).toBeNull();
  });
});

describe('extractForgejoUrls', () => {
  const hosts = ['code.example.com'];

  it('extracts and dedupes previewable URLs', () => {
    const text =
      'see https://code.example.com/a/b/pulls/1 and https://code.example.com/a/b/pulls/1 ' +
      'plus https://code.example.com/a/b/issues/2.';
    expect(extractForgejoUrls(text, hosts)).toEqual([
      'https://code.example.com/a/b/pulls/1',
      'https://code.example.com/a/b/issues/2',
    ]);
  });

  it('ignores other hosts and non-previewable paths', () => {
    const text = 'https://github.com/a/b/pull/1 https://code.example.com/a/b/tree/main';
    expect(extractForgejoUrls(text, hosts)).toEqual([]);
  });

  it('returns empty when no hosts configured', () => {
    expect(extractForgejoUrls('https://code.example.com/a/b/pulls/1', [])).toEqual([]);
  });
});
