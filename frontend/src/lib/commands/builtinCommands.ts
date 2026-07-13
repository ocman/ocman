import type { SlashCommand } from '../api';
import type { SessionModelEntry } from '../api.types';

/**
 * Whether the effective model exposes any reasoning variants. Drives the
 * /variants command (#300): mirroring OpenCode, the command is hidden when
 * the model has no variants. A "variant" is one of the model's reasoning /
 * thinking-budget levels, carried on `SessionModelEntry.reasoning`.
 */
export function modelHasVariants(
  selectedModel: string | undefined,
  modelEntries: SessionModelEntry[] | undefined,
): boolean {
  if (!selectedModel) return false;
  const match = modelEntries?.find(
    (e) => e.provider && `${e.provider}/${e.model}` === selectedModel,
  );
  return !!match?.reasoning?.length;
}

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
  { name: 'agents', description: 'Change the active agent (alias for /agent)' },
  { name: 'archive', description: 'Archive this session and open the most recent one' },
  { name: 'clear', description: 'Archive this session and start a new one in the same project directory' },
  { name: 'compact', description: 'Summarize conversation history to free up context window' },
  { name: 'copy', description: 'Copy the session transcript to the clipboard' },
  { name: 'details', description: 'Toggle visibility of tool-execution detail blocks' },
  { name: 'export', description: 'Download this conversation as a Markdown file' },
  { name: 'fork', description: 'Fork this session into a new child session' },
  { name: 'help', description: 'Show all available slash commands' },
  { name: 'model', description: 'Change the active model (opens a picker)' },
  { name: 'move', description: 'Move this session to another project directory (opens a picker)' },
  { name: 'new', description: 'Start a new session in the same project directory (optionally add a title)' },
  { name: 'rename', description: 'Rename this session' },
  { name: 'restart-opencode', description: 'Restart the tmux-managed OpenCode process for this session' },
  { name: 'share', description: 'Copy this ocman session URL to the clipboard (reachable only by clients that can access this ocman instance)' },
  { name: 'skills', description: 'Insert a skill command into the prompt (opens a picker)' },
  { name: 'sessions', description: 'Open the session switcher (aliases: resume, continue)' },
  { name: 'resume', description: 'Open the session switcher' },
  { name: 'continue', description: 'Open the session switcher' },
  { name: 'thinking', description: 'Toggle whether assistant reasoning blocks are shown (display-only)' },
  { name: 'tmux', description: 'Switch to the tmux session for this project' },
  { name: 'variants', description: 'Switch the current model reasoning variant (opens a picker)' },
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
