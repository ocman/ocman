import { describe, it, expect } from 'vitest';
import { BUILTIN_COMMANDS, KNOWN_AGENTS, modelHasVariants } from './builtinCommands';
import type { SessionModelEntry } from '../api.types';

describe('BUILTIN_COMMANDS', () => {
  it('exposes the canonical built-in slash commands', () => {
    const names = BUILTIN_COMMANDS.map((c) => c.name);
    expect(names).toEqual([
      'agent',
      'agents',
      'archive',
      'clear',
      'compact',
      'copy',
      'details',
      'export',
      'fork',
      'help',
      'model',
      'move',
      'new',
      'rename',
      'restart-opencode',
      'share',
      'skills',
      'sessions',
      'resume',
      'continue',
      'thinking',
      'tmux',
      'variants',
      'wt',
      'vscode',
    ]);
  });

  it('includes the /variants command (#300) for switching the model reasoning variant', () => {
    const variants = BUILTIN_COMMANDS.find((c) => c.name === 'variants');
    expect(variants).toBeDefined();
    expect(variants!.description).toMatch(/variant/i);
  });

  it('every command has a non-empty description', () => {
    for (const cmd of BUILTIN_COMMANDS) {
      expect(cmd.description).toBeTruthy();
      expect((cmd.description as string).length).toBeGreaterThan(0);
    }
  });

  it('command names are unique', () => {
    const names = BUILTIN_COMMANDS.map((c) => c.name);
    expect(new Set(names).size).toBe(names.length);
  });
});

describe('modelHasVariants (#300 — /variants gate)', () => {
  const entry = (over: Partial<SessionModelEntry>): SessionModelEntry => ({
    provider: 'anthropic',
    model: 'claude',
    ...over,
  });

  it('is true when the selected model exposes reasoning variants', () => {
    const entries = [entry({ provider: 'anthropic', model: 'claude', reasoning: ['low', 'high'] })];
    expect(modelHasVariants('anthropic/claude', entries)).toBe(true);
  });

  it('is false when the selected model has no reasoning variants', () => {
    const entries = [entry({ provider: 'anthropic', model: 'claude', reasoning: [] })];
    expect(modelHasVariants('anthropic/claude', entries)).toBe(false);
  });

  it('is false when the model entry omits reasoning entirely', () => {
    const entries = [entry({ provider: 'anthropic', model: 'claude' })];
    expect(modelHasVariants('anthropic/claude', entries)).toBe(false);
  });

  it('is false when no model is selected', () => {
    const entries = [entry({ provider: 'anthropic', model: 'claude', reasoning: ['low'] })];
    expect(modelHasVariants('', entries)).toBe(false);
    expect(modelHasVariants(undefined, entries)).toBe(false);
  });

  it('is false when the selected model is not in the entries', () => {
    const entries = [entry({ provider: 'anthropic', model: 'claude', reasoning: ['low'] })];
    expect(modelHasVariants('openai/gpt', entries)).toBe(false);
  });

  it('is false when entries are missing', () => {
    expect(modelHasVariants('anthropic/claude', undefined)).toBe(false);
  });
});

describe('KNOWN_AGENTS', () => {
  it('contains only OpenCode built-in primary agents (custom agents come from the live catalog)', () => {
    expect(KNOWN_AGENTS).toEqual(['build', 'plan']);
  });

  it('agent names are unique', () => {
    expect(new Set(KNOWN_AGENTS).size).toBe(KNOWN_AGENTS.length);
  });
});
