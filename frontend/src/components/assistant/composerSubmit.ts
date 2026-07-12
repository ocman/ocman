/**
 * Pure routing helper for the composer's Enter handler. Given the raw
 * input text and the active platform's capabilities, decides whether
 * the submission is a normal LLM prompt (`send`), a slash-command
 * dispatch (`command`), a raw shell command (`shell`), or should be
 * silently ignored (`noop`).
 *
 * Centralising the decision here keeps the Composer's keydown
 * handler small and lets us TDD the routing — including the
 * shellExec capability gate that disables `!`-prefix shell mode on
 * platforms without a shell-tool primitive (e.g. Claude Code).
 */

export type ComposerSubmitRoute =
  | { kind: 'noop' }
  | { kind: 'send'; text: string }
  | { kind: 'command'; command: string; args: string }
  | { kind: 'shell'; command: string };

export interface ComposerSubmitCaps {
  /** Whether the active platform supports raw shell execution. */
  shellExec: boolean;
}

export function routeComposerSubmit(
  raw: string,
  caps: ComposerSubmitCaps,
): ComposerSubmitRoute {
  const trimmed = raw.trim();
  if (!trimmed) return { kind: 'noop' };

  // `!`-prefix → raw shell, but only when the platform reports
  // caps.shellExec. On platforms without it (Claude Code) we keep
  // today's behaviour and let the LLM see the literal `!ls` prompt.
  if (trimmed.startsWith('!') && caps.shellExec) {
    const command = trimmed.slice(1).trim();
    if (!command) return { kind: 'noop' };
    return { kind: 'shell', command };
  }

  // `/`-prefix → slash command. The bit before the first space is
  // the command name; everything after is verbatim arguments.
  if (trimmed.startsWith('/')) {
    const spaceIdx = trimmed.indexOf(' ');
    const raw = spaceIdx > 0 ? trimmed.slice(1, spaceIdx) : trimmed.slice(1);
    const args = spaceIdx > 0 ? trimmed.slice(spaceIdx + 1).trim() : '';
    // `/agents` is an OpenCode-parity alias for the `/agent` picker (#295).
    const command = raw === 'agents' ? 'agent' : raw;
    return { kind: 'command', command, args };
  }

  return { kind: 'send', text: trimmed };
}
