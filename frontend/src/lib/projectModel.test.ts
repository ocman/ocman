import { describe, it, expect, beforeEach } from 'vitest';
import { getProjectModel, saveProjectModel } from './projectModel';

// Lightweight localStorage stub (project runs vitest without a DOM env).
function installLocalStorageStub() {
  const data = new Map<string, string>();
  const stub = {
    getItem: (k: string) => (data.has(k) ? data.get(k)! : null),
    setItem: (k: string, v: string) => { data.set(k, String(v)); },
    removeItem: (k: string) => { data.delete(k); },
    clear: () => { data.clear(); },
    key: (i: number) => Array.from(data.keys())[i] ?? null,
    get length() { return data.size; },
  };
  (globalThis as unknown as { window: unknown }).window = { localStorage: stub };
}

describe('projectModel', () => {
  beforeEach(() => installLocalStorageStub());

  it('round-trips a model per directory', () => {
    saveProjectModel('/a', 'anthropic/opus');
    saveProjectModel('/b', 'openai/gpt');
    expect(getProjectModel('/a')).toBe('anthropic/opus');
    expect(getProjectModel('/b')).toBe('openai/gpt');
  });

  it('returns empty for unknown directory', () => {
    expect(getProjectModel('/nope')).toBe('');
  });

  it('overwrites the previous model for a directory', () => {
    saveProjectModel('/a', 'first');
    saveProjectModel('/a', 'second');
    expect(getProjectModel('/a')).toBe('second');
  });

  it('ignores empty directory or model', () => {
    saveProjectModel('', 'x');
    saveProjectModel('/a', '');
    expect(getProjectModel('')).toBe('');
    expect(getProjectModel('/a')).toBe('');
  });
});
