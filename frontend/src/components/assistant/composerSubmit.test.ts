import { describe, it, expect } from 'vitest';
import { routeComposerSubmit, type ComposerSubmitRoute } from './composerSubmit';

describe('routeComposerSubmit', () => {
  it('routes plain text to send', () => {
    const got = routeComposerSubmit('hello there', { shellExec: true });
    expect(got).toEqual<ComposerSubmitRoute>({ kind: 'send', text: 'hello there' });
  });

  it('routes /-prefixed text to command with args split on first space', () => {
    const got = routeComposerSubmit('/rename my new title', { shellExec: true });
    expect(got).toEqual<ComposerSubmitRoute>({
      kind: 'command',
      command: 'rename',
      args: 'my new title',
    });
  });

  it('routes /-prefixed text without args to command with empty args', () => {
    const got = routeComposerSubmit('/compact', { shellExec: true });
    expect(got).toEqual<ComposerSubmitRoute>({
      kind: 'command',
      command: 'compact',
      args: '',
    });
  });

  it('routes !-prefixed text to shell when shellExec is true', () => {
    const got = routeComposerSubmit('!ls -la', { shellExec: true });
    expect(got).toEqual<ComposerSubmitRoute>({
      kind: 'shell',
      command: 'ls -la',
    });
  });

  it('strips internal whitespace after !', () => {
    // Leading space after `!` is part of the command and should be
    // trimmed so OpenCode doesn't choke on " ls".
    const got = routeComposerSubmit('!  ls', { shellExec: true });
    expect(got).toEqual<ComposerSubmitRoute>({
      kind: 'shell',
      command: 'ls',
    });
  });

  it('drops empty !-only input (single bang)', () => {
    expect(routeComposerSubmit('!', { shellExec: true })).toEqual<ComposerSubmitRoute>({ kind: 'noop' });
    expect(routeComposerSubmit('!   ', { shellExec: true })).toEqual<ComposerSubmitRoute>({ kind: 'noop' });
  });

  it('falls back to send when shellExec is false (Claude Code, etc.)', () => {
    const got = routeComposerSubmit('!ls -la', { shellExec: false });
    expect(got).toEqual<ComposerSubmitRoute>({ kind: 'send', text: '!ls -la' });
  });

  it('drops fully blank input', () => {
    expect(routeComposerSubmit('', { shellExec: true })).toEqual<ComposerSubmitRoute>({ kind: 'noop' });
    expect(routeComposerSubmit('   \n', { shellExec: true })).toEqual<ComposerSubmitRoute>({ kind: 'noop' });
  });
});
