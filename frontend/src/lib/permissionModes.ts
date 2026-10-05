import type { PermissionRule } from './api.types';

/**
 * A named permission posture the user can pick from the session
 * header's lock menu. Each maps to a concrete per-session ruleset
 * sent to PUT /api/session/{id}/permission-rules. An empty ruleset
 * restores the platform's configured defaults.
 */
export interface PermissionMode {
  id: string;
  label: string;
  description: string;
  rules: PermissionRule[];
  /** Marks postures worth an extra confirmation (e.g. yolo). */
  dangerous?: boolean;
}

export const PERMISSION_MODES: PermissionMode[] = [
  {
    id: 'default',
    label: 'Default',
    description: 'Inherit the configured permission defaults',
    rules: [],
  },
  {
    id: 'plan',
    label: 'Plan only',
    description: 'Deny file edits and shell commands',
    rules: [
      { permission: 'edit', pattern: '*', action: 'deny' },
      { permission: 'bash', pattern: '*', action: 'deny' },
    ],
  },
  {
    id: 'auto-edit',
    label: 'Auto-accept edits',
    description: 'Allow file edits, ask for shell commands',
    rules: [
      { permission: 'edit', pattern: '*', action: 'allow' },
      { permission: 'bash', pattern: '*', action: 'ask' },
    ],
  },
  {
    id: 'yolo',
    label: 'YOLO',
    description: 'Allow everything without asking',
    dangerous: true,
    rules: [{ permission: '*', pattern: '*', action: 'allow' }],
  },
];

const ruleKey = (r: PermissionRule) => `${r.permission}\u0000${r.pattern}\u0000${r.action}`;

function sameRuleset(a: PermissionRule[], b: PermissionRule[]): boolean {
  if (a.length !== b.length) return false;
  // Compare as multisets: a repeated rule must not stand in for a missing one.
  const sortedKeys = (rules: PermissionRule[]) => rules.map(ruleKey).sort();
  const keysB = sortedKeys(b);
  return sortedKeys(a).every((key, i) => key === keysB[i]);
}

/**
 * Maps a session's current ruleset back to a preset id, or 'custom'
 * when it doesn't match any preset (e.g. hand-written rules or rules
 * set by another tool). Order-insensitive.
 *
 * OpenCode appends a PATCHed ruleset to the session's existing rules and
 * evaluates the last match, so after switching modes the ruleset ends with
 * the latest preset (e.g. auto-edit rules followed by YOLO's `* allow`).
 * A preset found at the tail is the mode in effect.
 */
export function classifyPermissionMode(rules: PermissionRule[]): string {
  const match =
    PERMISSION_MODES.find((m) => sameRuleset(m.rules, rules)) ??
    PERMISSION_MODES.find((m) => m.rules.length > 0 && sameRuleset(m.rules, rules.slice(-m.rules.length)));
  return match ? match.id : 'custom';
}
