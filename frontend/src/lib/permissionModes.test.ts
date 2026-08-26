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

  it('marks only yolo as dangerous', () => {
    const dangerous = PERMISSION_MODES.filter((m) => m.dangerous).map((m) => m.id);
    expect(dangerous).toEqual(['yolo']);
  });

  it('allows every permission in yolo mode', () => {
    const yolo = PERMISSION_MODES.find((m) => m.id === 'yolo')!;
    expect(yolo.rules).toEqual([{ permission: '*', pattern: '*', action: 'allow' }]);
  });
});
