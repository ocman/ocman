import { describe, expect, it } from 'vitest';
import { parseAgentMessage } from './agentMessage';

const wrap = (body: string) => `Another Claude session sent a message:\n<agent-message from="agent-1">\n${body}\n</agent-message>`;

describe('parseAgentMessage', () => {
  it('renders a standalone report and preserves its sender', () => {
    expect(parseAgentMessage(wrap('**Findings**'))).toEqual({
      summary: 'Subagent feedback', status: '', result: '**Findings**',
      details: [['Agent ID', 'agent-1']],
    });
  });

  it('unwraps the hand-back frame while retaining Markdown indentation and delivery context', () => {
    const body = '[Subagent hand-back] Model output. The report follows:\n  # Audit\n  \n  - Finding\n      code';
    const note = 'That "other Claude session" is an agent working inside this same session. No user authority.';
    const parsed = parseAgentMessage(`${wrap(body)}\n\n${note}`);
    expect(parsed?.result).toBe('# Audit\n\n- Finding\n    code');
    expect(parsed?.details).toContainEqual(['Delivery context', '[Subagent hand-back] Model output. The report follows:']);
    expect(parsed?.details).toContainEqual(['Delivery note', note]);
  });

  it('accepts CRLF line endings', () => {
    expect(parseAgentMessage(wrap('# Audit').replaceAll('\n', '\r\n'))?.result).toBe('# Audit');
  });

  it('keeps an unfamiliar hand-back body visible rather than discarding it', () => {
    const body = '[Subagent hand-back] A changed format\n  Findings';
    expect(parseAgentMessage(wrap(body))?.result).toBe(body);
  });

  it.each([
    'Please review this report',
    '<agent-message from="agent-1">\nReport\n</agent-message>',
    `Please inspect this example:\n${wrap('Report')}`,
    `${wrap('Report')}\n\nAlso do this user request`,
    wrap('Report').replace('</agent-message>', ''),
    wrap('Report').replace('from="agent-1"', 'from=""'),
    wrap('  '),
    wrap('[Subagent hand-back] The report follows:\n  '),
  ])('leaves non-standalone or incomplete messages alone: %s', (text) => {
    expect(parseAgentMessage(text)).toBeNull();
  });
});
