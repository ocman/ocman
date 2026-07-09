import type { SlashCommand } from '../api';

/**
 * Slash commands that the composer renders even when the platform's
 * `/api/slash-commands` endpoint returns nothing. These are the ocman
 * built-ins that operate on the page itself (not on a remote agent),
 * so they exist regardless of whether the platform is live.
 *
 * The fetch path merges this list with platform-provided commands:
 * platform commands always win on conflicts, so a project-supplied
 * `/agent` command can override the built-in description.
 */
export const BUILTIN_COMMANDS: SlashCommand[] = [
  { name: 'agent', description: 'Change the active agent (opens a picker)' },
  { name: 'archive', description: 'Archive this session and open the most recent one' },
  { name: 'clear', description: 'Archive this session and start a new one in the same project directory' },
  { name: 'compact', description: 'Summarize conversation history to free up context window' },
  { name: 'model', description: 'Change the active model (opens a picker)' },
  { name: 'new', description: 'Start a new session in the same project directory (optionally add a title)' },
  { name: 'rename', description: 'Rename this session' },
  { name: 'restart-opencode', description: 'Restart the tmux-managed OpenCode process for this session' },
  { name: 'tmux', description: 'Switch to the tmux session for this project' },
  { name: 'wt', description: 'Create a worktree session for this project (optionally prefill the branch)' },
  { name: 'vscode', description: 'Open the project directory in VS Code' },
];

/**
 * OpenCode's built-in primary agents, offered in the `/agent` picker
 * even when the live catalog hasn't loaded yet (or returns nothing).
 * Everything else — custom user/project agents — comes from the live
 * `/agent` catalog, which is the source of truth. Order matters: first
 * entry is the default highlight.
 */
export const KNOWN_AGENTS: readonly string[] = ['build', 'plan'];
