import { describe, expect, it } from 'vitest';
import { classifyPermissionMode, PERMISSION_MODES } from './permissionModes';
import type { PermissionRule } from './api.types';

describe('classifyPermissionMode', () => {
  it('classifies an empty ruleset as default', () => {
    expect(classifyPermissionMode([])).toBe('default');
  });

  it('round-trips every preset', () => {
    for (const mode of PERMISSION_MODES) {
      expect(classifyPermissionMode(mode.rules)).toBe(mode.id);
    }
  });

  it('is order-insensitive', () => {
    const plan = PERMISSION_MODES.find((m) => m.id === 'plan')!;
    const reversed = [...plan.rules].reverse();
    expect(classifyPermissionMode(reversed)).toBe('plan');
  });

  it('returns custom for unknown rulesets', () => {
    const rules: PermissionRule[] = [{ permission: 'edit', pattern: 'src/*', action: 'deny' }];
    expect(classifyPermissionMode(rules)).toBe('custom');
  });

  it('returns custom for a superset of a preset', () => {
    const plan = PERMISSION_MODES.find((m) => m.id === 'plan')!;
    const rules = [...plan.rules, { permission: 'webfetch', pattern: '*', action: 'deny' } as PermissionRule];
    expect(classifyPermissionMode(rules)).toBe('custom');
  });

  // OpenCode appends PATCHed rules to the session's ruleset instead of
  // replacing it, and the last matching rule wins, so the mode picked last
  // is the ruleset's tail.
  it('classifies by the most recently appended preset', () => {
    const autoEdit = PERMISSION_MODES.find((m) => m.id === 'auto-edit')!;
    const yolo = PERMISSION_MODES.find((m) => m.id === 'yolo')!;
    const plan = PERMISSION_MODES.find((m) => m.id === 'plan')!;
    expect(classifyPermissionMode([...autoEdit.rules, ...yolo.rules])).toBe('yolo');
    expect(classifyPermissionMode([...yolo.rules, ...plan.rules])).toBe('plan');
  });

  it('does not let a repeated rule stand in for a missing preset rule', () => {
    const bashDeny: PermissionRule = { permission: 'bash', pattern: '*', action: 'deny' };
    const rules: PermissionRule[] = [{ permission: '*', pattern: '*', action: 'allow' }, bashDeny, bashDeny];
    expect(classifyPermissionMode(rules)).toBe('custom');
  });

  it('marks only yolo as dangerous', () => {
    const dangerous = PERMISSION_MODES.filter((m) => m.dangerous).map((m) => m.id);
    expect(dangerous).toEqual(['yolo']);
  });

  it('allows every permission in yolo mode', () => {
    const yolo = PERMISSION_MODES.find((m) => m.id === 'yolo')!;
    expect(yolo.rules).toEqual([{ permission: '*', pattern: '*', action: 'allow' }]);
  });
});
