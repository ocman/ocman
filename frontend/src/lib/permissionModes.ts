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
    // ponytail: explicit permission list, not "*" — OpenCode's wildcard
    // semantics across permission *names* are unverified.
    rules: [
      { permission: 'edit', pattern: '*', action: 'allow' },
      { permission: 'bash', pattern: '*', action: 'allow' },
      { permission: 'webfetch', pattern: '*', action: 'allow' },
    ],
  },
];

const ruleKey = (r: PermissionRule) => `${r.permission}\u0000${r.pattern}\u0000${r.action}`;

function sameRuleset(a: PermissionRule[], b: PermissionRule[]): boolean {
  if (a.length !== b.length) return false;
  const keys = new Set(a.map(ruleKey));
  return b.every((r) => keys.has(ruleKey(r)));
}

/**
 * Maps a session's current ruleset back to a preset id, or 'custom'
 * when it doesn't match any preset (e.g. hand-written rules or rules
 * set by another tool). Order-insensitive.
 */
export function classifyPermissionMode(rules: PermissionRule[]): string {
  const match = PERMISSION_MODES.find((m) => sameRuleset(m.rules, rules));
  return match ? match.id : 'custom';
}
