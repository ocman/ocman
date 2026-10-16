import { describe, expect, it } from 'vitest';
import { parseSubagentEnvelope } from './subagentEnvelope';

describe('parseSubagentEnvelope', () => {
  it('reads the tool-result form without a description', () => {
    expect(parseSubagentEnvelope('<subagent sessionID="ses_1" state="completed">\n**Done**\n</subagent>'))
      .toEqual({ sessionID: 'ses_1', status: 'completed', description: 'Subagent task', output: '**Done**' });
  });
  it.each(['completed', 'error', 'cancelled'])('reads the synthetic %s form with raw quotes', (status) => {
    expect(parseSubagentEnvelope(`<subagent sessionID="ses_1" state="${status}" description="Review "a & b"">\nresult\n</subagent>`))
      .toEqual({ sessionID: 'ses_1', status, description: 'Review "a & b"', output: 'result' });
  });
  it.each([
    null, {}, '',
    'Example: <subagent sessionID="ses_1" state="completed">\nresult\n</subagent>',
    '<subagent sessionID="ses_1" state="completed">\npartial',
    '<subagent state="completed">\nresult\n</subagent>',
    '<subagent sessionID="ses_1" state="unknown">\nresult\n</subagent>',
    '<subagent sessionID="ses_1" state="completed">\n<subagent>nested</subagent>\n</subagent>',
  ])('does not consume malformed or embedded text: %s', (text) => {
    expect(parseSubagentEnvelope(text)).toBeNull();
  });
});
