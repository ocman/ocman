import { describe, expect, it } from 'vitest';
import { basePlatform, modelRefForSession, modelRefPlatform, qualifyModelRef, splitModelRef } from './modelRef';

describe('model references', () => {
  it('splits only a prefix before the first slash', () => {
    expect(splitModelRef('claudecode:anthropic/sonnet')).toEqual({ platform: 'claudecode', model: 'anthropic/sonnet' });
    expect(splitModelRef('openrouter/meta-llama/llama-3:free')).toEqual({ platform: '', model: 'openrouter/meta-llama/llama-3:free' });
    expect(splitModelRef('sonnet')).toEqual({ platform: '', model: 'sonnet' });
  });
  it('qualifies and resolves platforms', () => {
    expect(qualifyModelRef('opencode', 'a/b')).toBe('a/b');
    expect(qualifyModelRef('claudecode', 'a/b')).toBe('claudecode:a/b');
    expect(qualifyModelRef('claudecode', '')).toBe('');
    expect(modelRefPlatform('a/b')).toBe('opencode');
    expect(modelRefPlatform('claudecode:a/b')).toBe('claudecode');
    expect(modelRefPlatform('')).toBe('');
    expect(basePlatform('r-box:claudecode')).toBe('claudecode');
  });
  it('fits a reference to an existing session', () => {
    expect(modelRefForSession('claudecode:a/b', 'r-box:claudecode')).toBe('a/b');
    expect(modelRefForSession('claudecode:a/b', 'opencode')).toBe('');
    expect(modelRefForSession('a/b', 'claudecode')).toBe('a/b');
  });
});
