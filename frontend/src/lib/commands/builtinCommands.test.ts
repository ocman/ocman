import { describe, it, expect } from 'vitest';
import { BUILTIN_COMMANDS, KNOWN_AGENTS } from './builtinCommands';

describe('BUILTIN_COMMANDS', () => {
  it('exposes the canonical built-in slash commands', () => {
    const names = BUILTIN_COMMANDS.map((c) => c.name);
    expect(names).toEqual([
      'agent',
      'archive',
      'clear',
      'compact',
      'model',
      'new',
      'rename',
      'restart-opencode',
      'tmux',
      'wt',
      'vscode',
    ]);
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

describe('KNOWN_AGENTS', () => {
  it('contains the canonical default agents in the expected order', () => {
    expect(KNOWN_AGENTS).toEqual([
      'build',
      'developer',
      'plan',
      'architect',
      'ba',
      'brainstormer',
      'reviewer',
      'security',
    ]);
  });

  it('agent names are unique', () => {
    expect(new Set(KNOWN_AGENTS).size).toBe(KNOWN_AGENTS.length);
  });
});
