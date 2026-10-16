import { describe, expect, it } from 'vitest';
import { mcpToolLabel } from './mcpToolLabel';

const label = (code: string) => mcpToolLabel('execute', JSON.stringify({ code }));

describe('mcpToolLabel', () => {
  it.each([
    ["return await tools.ocman.sessions({action:'help'});", 'MCP ocman.sessions'],
    ["return tools['chrome-devtools'].list_pages({});", 'MCP chrome-devtools.list_pages'],
    ['return tools["ocman"]["sessions"]({});', 'MCP ocman.sessions'],
    ['await tools.ocman.sessions({}); await tools.ocman.sessions({}); await tools.homelab.user_info({});', 'MCP ocman.sessions, homelab.user_info'],
    ["return search({query:'chrome-devtools'});", 'MCP tool discovery'],
    ['search({}); return tools.ocman.sessions({});', 'MCP ocman.sessions'],
    ['// tools.fake.call({})\n/* search({}) */ return 1;', null],
    ['return "tools.fake.call({})";', null],
    ["return 'search({})';", null],
    ['return `tools.fake.call({})`;', null],
    ['return tools[server][method]({});', null],
    ['return 1;', null],
  ])('summarizes %s', (code, expected) => {
    expect(label(code)).toBe(expected);
  });

  it('accepts the MCP-prefixed wrapper name', () => {
    expect(mcpToolLabel('mcp_execute', JSON.stringify({ code: 'tools.ocman.sessions({})' }))).toBe('MCP ocman.sessions');
  });

  it.each(['{}', 'null', '{', '{"code":123}'])('ignores missing or invalid code in %s', (args) => {
    expect(mcpToolLabel('execute', args)).toBeNull();
  });

  it('does not relabel unrelated tools', () => {
    expect(mcpToolLabel('bash', JSON.stringify({ code: 'tools.ocman.sessions({})' }))).toBeNull();
  });
});
