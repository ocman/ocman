import { describe, expect, it } from 'vitest';
import { mcpToolIntent } from './mcpToolIntent';

describe('mcpToolIntent', () => {
  it.each(['ocman_sessions', 'mcp_ocman_sessions', 'ocman.sessions', 'mcp__ocman__sessions'])('reads intent from %s', (name) => {
    expect(mcpToolIntent(name, JSON.stringify({ intent: ' Find  the active session \n' }))).toBe('Find the active session');
  });

  it.each([
    ['return tools.ocman.sessions({action: "list", intent: "Find a session"});', 'Find a session'],
    ["tools['ocman']['sessions']({'intent': 'Find a session'});", 'Find a session'],
    ['tools.ocman.sessions({intent: "Find \\"quoted\\" sessions"});', 'Find "quoted" sessions'],
    ["tools.ocman.sessions({intent: 'Find the user\\'s session'});", "Find the user's session"],
    ['tools.ocman.sessions({intent: "One"}); tools.ocman.models({intent: "Two"});', 'One; Two'],
    ['tools.ocman.sessions({intent: "One"}); tools.ocman.models({intent: "One"});', 'One'],
    ['// tools.ocman.sessions({intent: "Fake"})\n tools.ocman.sessions({intent: "Real"})', 'Real'],
    ['"tools.ocman.sessions({intent: \'Fake\'})"', null],
    ['tools.other.sessions({intent: "Other"})', null],
    ['tools.ocman.sessions({data: {intent: "Nested"}})', null],
    ['tools.ocman.sessions({intent: reason})', null],
    ['tools.ocman.sessions({intent: "Prefix" + reason})', null],
    ['tools.ocman.sessions({intent: `Dynamic ${reason}`})', null],
    ['tools[server].sessions({intent: "Dynamic"})', null],
    ['tools["ocman".sessions({intent: "Invalid"})', null],
    ['tools.ocman({intent: "Not a tool"})', null],
    ['tools.ocman.sessions({intent: "Invalid\\xZZ"})', null],
    ['/* tools.ocman.sessions({intent: "Fake"}) */ tools.ocman.sessions({intent: "Real"})', 'Real'],
    ['object.tools.ocman.sessions({intent: "Not the tool global"})', null],
    ['tools.ocman.sessions({intent: " "})', null],
  ])('extracts only literal ocman intents from %s', (code, expected) => {
    expect(mcpToolIntent('execute', JSON.stringify({ code }))).toBe(expected);
    expect(mcpToolIntent('mcp_execute', JSON.stringify({ code }))).toBe(expected);
  });

  it.each(['{', 'null', '[]', '{}', '{"intent":123}', '{"intent":" "}'])('ignores invalid intent in %s', (args) => {
    expect(mcpToolIntent('ocman_sessions', args)).toBeNull();
    expect(mcpToolIntent('execute', args)).toBeNull();
  });

  it('does not interpret unrelated tool input', () => {
    expect(mcpToolIntent('bash', '{"intent":"Ignore"}')).toBeNull();
    expect(mcpToolIntent('execute', '{"code":12}')).toBeNull();
  });
});
