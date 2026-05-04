import { describe, it, expect } from 'vitest';
import {
  MUTED_TOOL_NAMES,
  MUTED_LINE_TOOL_NAMES,
  isMutedTool,
  isMutedLineTool,
} from './mutedTools';

describe('MUTED_TOOL_NAMES', () => {
  it('contains the OpenCode + Claude variants of read/grep/glob/webfetch', () => {
    for (const name of [
      '__read__', 'read', 'mcp_read',
      'grep', 'mcp_grep',
      'glob', 'mcp_glob',
      'webfetch', 'mcp_webfetch', 'mcp_Webfetch',
    ]) {
      expect(MUTED_TOOL_NAMES.has(name)).toBe(true);
    }
  });

  it('does not contain unrelated tool names', () => {
    expect(MUTED_TOOL_NAMES.has('write')).toBe(false);
    expect(MUTED_TOOL_NAMES.has('edit')).toBe(false);
    expect(MUTED_TOOL_NAMES.has('task')).toBe(false);
    expect(MUTED_TOOL_NAMES.has('__skill__')).toBe(false);
  });
});

describe('MUTED_LINE_TOOL_NAMES', () => {
  it('is a strict superset of MUTED_TOOL_NAMES', () => {
    for (const name of MUTED_TOOL_NAMES) {
      expect(MUTED_LINE_TOOL_NAMES.has(name)).toBe(true);
    }
    expect(MUTED_LINE_TOOL_NAMES.size).toBe(MUTED_TOOL_NAMES.size + 1);
  });

  it('adds __skill__', () => {
    expect(MUTED_LINE_TOOL_NAMES.has('__skill__')).toBe(true);
  });
});

describe('isMutedTool', () => {
  it('returns true for muted names', () => {
    expect(isMutedTool('read')).toBe(true);
    expect(isMutedTool('mcp_Webfetch')).toBe(true);
  });

  it('returns false for non-muted names and falsy inputs', () => {
    expect(isMutedTool('write')).toBe(false);
    expect(isMutedTool('__skill__')).toBe(false); // not in the base set
    expect(isMutedTool('')).toBe(false);
    expect(isMutedTool(undefined)).toBe(false);
    expect(isMutedTool(null)).toBe(false);
  });
});

describe('isMutedLineTool', () => {
  it('matches __skill__ in addition to the base set', () => {
    expect(isMutedLineTool('__skill__')).toBe(true);
    expect(isMutedLineTool('read')).toBe(true);
  });

  it('returns false for non-muted names and falsy inputs', () => {
    expect(isMutedLineTool('write')).toBe(false);
    expect(isMutedLineTool(undefined)).toBe(false);
  });
});
