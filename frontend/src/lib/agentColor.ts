// agentColor resolves an agent's OpenCode-configured `color` value into a CSS
// color string usable in the UI. OpenCode accepts either a hex color (e.g.
// `#ff6b6b`) or a semantic theme name (`primary`, `secondary`, `accent`,
// `success`, `warning`, `error`, `info`).
//
// When the agent has no configured color, or when no agents have been loaded
// yet (e.g. no running OpenCode instance for this directory), a deterministic
// fallback is derived from the agent name so each agent still gets a stable,
// distinguishable accent.

import { createContext, useContext } from 'react';
import type { AgentInfo } from './api';

// Theme names -> CSS variables from tokens.css.
// OpenCode's semantic palette is mapped to the ocman Catppuccin tokens.
const THEME_COLORS: Record<string, string> = {
  primary: 'var(--accent)',
  secondary: 'var(--accent3)',
  accent: 'var(--accent)',
  success: 'var(--accent2)',
  warning: 'var(--accent4)',
  error: 'var(--danger)',
  info: 'var(--sapphire)',
};

// Fallback palette used when an agent has no configured color. Ordered to
// produce visually distinct neighbours. Uses Catppuccin tokens so the palette
// reacts to future theme changes.
const FALLBACK_PALETTE = [
  'var(--accent)',    // blue
  'var(--accent2)',   // green
  'var(--accent3)',   // mauve
  'var(--accent4)',   // peach
  'var(--pink)',
  'var(--teal)',
  'var(--yellow)',
  'var(--sapphire)',
  'var(--lavender)',
  'var(--danger)',
];

// Built-in OpenCode agents get stable defaults even before we load the
// /agent API, so the UI is never colourless on first render. `build` uses
// the same mauve accent the composer's left border has always used, so that
// UI surface visually reads as "the build agent is active" by default.
const BUILTIN_DEFAULTS: Record<string, string> = {
  build: 'var(--accent3)',
  plan: 'var(--accent2)',
  general: 'var(--accent)',
  explore: 'var(--accent4)',
};

function isHexColor(value: string): boolean {
  return /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$/.test(value);
}

// Deterministic hash -> palette index so the same agent name always picks the
// same fallback color across reloads.
function hashString(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) {
    h = (h * 31 + s.charCodeAt(i)) | 0;
  }
  return Math.abs(h);
}

function fallbackColor(name: string): string {
  if (!name) return 'var(--text-dim)';
  const builtin = BUILTIN_DEFAULTS[name];
  if (builtin) return builtin;
  return FALLBACK_PALETTE[hashString(name) % FALLBACK_PALETTE.length];
}

// resolveAgentColor turns a raw `color` value (from the OpenCode API) into a
// CSS color. Unknown strings are returned as-is so custom CSS variable names
// (e.g. `var(--my-agent)`) would still work.
function resolveAgentColor(raw: string | undefined): string | undefined {
  if (!raw) return undefined;
  const trimmed = raw.trim();
  if (!trimmed) return undefined;
  if (isHexColor(trimmed)) return trimmed;
  const theme = THEME_COLORS[trimmed.toLowerCase()];
  if (theme) return theme;
  return trimmed;
}

export function agentColor(
  name: string,
  agents?: AgentInfo[] | null,
): string {
  if (!name) return 'var(--text-dim)';
  const match = agents?.find((a) => a.name === name);
  const resolved = resolveAgentColor(match?.color);
  return resolved ?? fallbackColor(name);
}

// Context providing the loaded agent list so deeply-nested components (message
// bubbles, composer dropdown options) can resolve colors without prop drilling.
export const AgentsContext = createContext<AgentInfo[]>([]);

export function useAgentColor(name: string | undefined | null): string {
  const agents = useContext(AgentsContext);
  if (!name) return 'var(--text-dim)';
  return agentColor(name, agents);
}
