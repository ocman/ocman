import { describe, expect, it } from 'vitest';
import type { AgentInfo } from './api';
import { agentColor } from './agentColor';

describe('agentColor', () => {
  describe('fallbacks (no agents loaded)', () => {
    it('returns dimmed color for empty name', () => {
      expect(agentColor('')).toBe('var(--text-dim)');
    });

    it('returns the mauve accent for the built-in build agent', () => {
      // This matches the composer's historical left-border color so a default
      // `build` session keeps its pre-feature appearance.
      expect(agentColor('build')).toBe('var(--accent3)');
    });

    it('returns a stable default for each remaining built-in', () => {
      expect(agentColor('plan')).toBe('var(--accent2)');
      expect(agentColor('general')).toBe('var(--accent)');
      expect(agentColor('explore')).toBe('var(--accent4)');
    });

    it('returns a deterministic palette color for unknown agent names', () => {
      const first = agentColor('my-custom-agent');
      const second = agentColor('my-custom-agent');
      expect(first).toBe(second);
      // Different names should, with very high probability, map to different
      // entries in the 10-color palette. We just assert it's a CSS var value.
      expect(first).toMatch(/^var\(--/);
    });
  });

  describe('with agents loaded from /agent API', () => {
    const agents: AgentInfo[] = [
      { name: 'build', color: '#ff6b6b' },
      { name: 'plan', color: 'primary' },
      { name: 'review', color: 'success' },
      { name: 'audit', color: 'error' },
      { name: 'info-agent', color: 'info' },
      { name: 'no-color' },
      { name: 'blank-color', color: '   ' },
      { name: 'upper-theme', color: 'SUCCESS' },
      { name: 'short-hex', color: '#abc' },
      { name: 'hex-alpha', color: '#12345678' },
    ];

    it('passes hex colors through unchanged', () => {
      expect(agentColor('build', agents)).toBe('#ff6b6b');
    });

    it('supports 3- and 8-digit hex values', () => {
      expect(agentColor('short-hex', agents)).toBe('#abc');
      expect(agentColor('hex-alpha', agents)).toBe('#12345678');
    });

    it('maps semantic theme names to CSS variables', () => {
      expect(agentColor('plan', agents)).toBe('var(--accent)');
      expect(agentColor('review', agents)).toBe('var(--accent2)');
      expect(agentColor('audit', agents)).toBe('var(--danger)');
      expect(agentColor('info-agent', agents)).toBe('var(--sapphire)');
    });

    it('matches theme names case-insensitively', () => {
      expect(agentColor('upper-theme', agents)).toBe('var(--accent2)');
    });

    it('falls back to the built-in default when the API omits a color', () => {
      // `no-color` is unknown so it hashes into the fallback palette.
      expect(agentColor('no-color', agents)).toMatch(/^var\(--/);
    });

    it('treats whitespace-only colors as missing', () => {
      // `blank-color` has `"   "`; should behave like no color was set.
      expect(agentColor('blank-color', agents)).toMatch(/^var\(--/);
    });

    it('falls back to the built-in default for known built-ins not returned by the API', () => {
      // Even though `plan` is in the API here, an API that omits `plan`
      // should still resolve to the stable built-in default.
      const withoutPlan = agents.filter((a) => a.name !== 'plan');
      expect(agentColor('plan', withoutPlan)).toBe('var(--accent2)');
    });

    it('returns unknown color strings verbatim so custom values still work', () => {
      const customAgents: AgentInfo[] = [
        { name: 'themed', color: 'var(--my-custom-token)' },
        { name: 'rgb', color: 'rgb(10, 20, 30)' },
      ];
      expect(agentColor('themed', customAgents)).toBe('var(--my-custom-token)');
      expect(agentColor('rgb', customAgents)).toBe('rgb(10, 20, 30)');
    });

    it('handles a null agent list the same as no list', () => {
      expect(agentColor('build', null)).toBe('var(--accent3)');
    });
  });
});
