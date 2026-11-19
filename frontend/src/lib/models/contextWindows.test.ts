import { describe, it, expect } from 'vitest';
import {
  MODEL_CONTEXT_WINDOWS,
  getContextWindow,
  formatTokenCount,
} from './contextWindows';

describe('MODEL_CONTEXT_WINDOWS', () => {
  it('contains a few representative known models', () => {
    expect(MODEL_CONTEXT_WINDOWS['gpt-4o']).toBe(128_000);
    expect(MODEL_CONTEXT_WINDOWS['claude-opus-4']).toBe(200_000);
    expect(MODEL_CONTEXT_WINDOWS['gemini-1.5-pro']).toBe(2_097_152);
  });
});

describe('getContextWindow', () => {
  it('returns null for falsy input', () => {
    expect(getContextWindow(undefined)).toBeNull();
    expect(getContextWindow('')).toBeNull();
  });

  it('returns the exact match when present', () => {
    expect(getContextWindow('claude-opus-4')).toBe(200_000);
    expect(getContextWindow('gpt-4')).toBe(8_192);
  });

  it('matches a provider-prefixed id via the suffix', () => {
    expect(getContextWindow('anthropic/claude-opus-4')).toBe(200_000);
    expect(getContextWindow('OpenAI/gpt-4o-mini')).toBe(128_000);
  });

  it('does not apply limits from a different model or variant', () => {
    expect(getContextWindow('claude-opus-4-FINETUNED')).toBeNull();
    expect(getContextWindow('anthropic/claude-opus-4-8')).toBeNull();
  });

  it('uses the full model id for variants with explicit entries', () => {
    expect(getContextWindow('gpt-4.1-mini')).toBe(1_047_576);
    expect(getContextWindow('openrouter/gpt-4.1-mini')).toBe(1_047_576);
  });

  it('returns null for unknown models', () => {
    expect(getContextWindow('made-up-model')).toBeNull();
    expect(getContextWindow('constructor')).toBeNull();
  });
});

describe('formatTokenCount', () => {
  it('renders values under 1000 as plain integers', () => {
    expect(formatTokenCount(0)).toBe('0');
    expect(formatTokenCount(42)).toBe('42');
    expect(formatTokenCount(999)).toBe('999');
  });

  it('renders thousands with one decimal place + K suffix', () => {
    expect(formatTokenCount(1_000)).toBe('1.0K');
    expect(formatTokenCount(12_345)).toBe('12.3K');
    expect(formatTokenCount(999_999)).toBe('1000.0K'); // rounds, no M yet
  });

  it('renders millions with one decimal place + M suffix', () => {
    expect(formatTokenCount(1_000_000)).toBe('1.0M');
    expect(formatTokenCount(1_234_567)).toBe('1.2M');
    expect(formatTokenCount(2_097_152)).toBe('2.1M');
  });
});
