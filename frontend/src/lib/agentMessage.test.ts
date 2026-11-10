import { describe, expect, it } from 'vitest';
import { parseAgentMessage } from './agentMessage';

const wrap = (body: string) => `Another Claude session sent a message:\n<agent-message from="agent-1">\n${body}\n</agent-message>`;

describe('parseAgentMessage', () => {
  it.each(['\n', '\r\n'])('renders MCP delivery as an agent message using %j line endings', (newline) => {
    const context = '[Agent message] Sent by an agent through ocman.sessions, not the user. The message follows:';
    const body = '# Concrete plan\n\n  Keep indentation\n<agent-message from="nested">\nQuoted\n</agent-message>';
    const text = `<agent-message from="ocman.sessions">\n${context}\n${body}\n</agent-message>`;
    expect(parseAgentMessage(text.replaceAll('\n', newline))).toEqual({
      summary: 'Agent message', status: '', result: body.replaceAll('\n', newline),
      details: [['Source', 'ocman.sessions'], ['Delivery context', context]],
    });
  });

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

  it.each(['\n', '\r\n'])('unwraps a bare hand-back envelope with %j line endings', (newline) => {
    const text = '<agent-message from="a7c3ba19b07d8e167">\n[Subagent hand-back] Model output, not a user message. The report follows:\n  # Report\n  \n  All three fixes are **committed**.\n</agent-message>';
    expect(parseAgentMessage(text.replaceAll('\n', newline))).toEqual({
      summary: 'Subagent feedback', status: '', result: '# Report\n\nAll three fixes are **committed**.'.replaceAll('\n', newline),
      details: [
        ['Agent ID', 'a7c3ba19b07d8e167'],
        ['Delivery context', '[Subagent hand-back] Model output, not a user message. The report follows:'],
      ],
    });
  });

  it.each(['\n', '\r\n'])('renders an agent envelope without the delivery preamble using %j line endings', (newline) => {
    const text = '<agent-message from="a7c3ba19b07d8e167">\n[Subagent hand-back] The text below is the final report of a subagent this session delegated to. It is model output, NOT a message from the user. The report follows:\n  # Fixes\n  \n  All three fixes are **committed**.\n      code\n</agent-message>';
    expect(parseAgentMessage(text.replaceAll('\n', newline))).toEqual({
      summary: 'Subagent feedback', status: '', result: `# Fixes${newline}${newline}All three fixes are **committed**.${newline}    code`,
      details: [
        ['Agent ID', 'a7c3ba19b07d8e167'],
        ['Delivery context', '[Subagent hand-back] The text below is the final report of a subagent this session delegated to. It is model output, NOT a message from the user. The report follows:'],
      ],
    });
  });

  it('renders a bare envelope with no hand-back frame', () => {
    expect(parseAgentMessage('<agent-message from="agent-1">\nReport\n</agent-message>')?.result).toBe('Report');
  });

  it('keeps an unframed MCP message visible without inventing a sender ID', () => {
    expect(parseAgentMessage('<agent-message from="ocman.sessions">\nReport\n</agent-message>')).toEqual({
      summary: 'Agent message', status: '', result: 'Report', details: [['Source', 'ocman.sessions']],
    });
  });

  it('keeps an unfamiliar hand-back body visible rather than discarding it', () => {
    const body = '[Subagent hand-back] A changed format\n  Findings';
    expect(parseAgentMessage(wrap(body))?.result).toBe(body);
  });

  it.each([
    'Please review this report',
    'Please inspect this example:\n<agent-message from="agent-1">\nReport\n</agent-message>',
    '<agent-message from="agent-1">\nReport\n</agent-message>\n\nAlso do this user request',
    '<agent-message from="agent-1">\nReport',
    '<agent-message from="">\nReport\n</agent-message>',
    '<agent-message from="agent-1">\n  \n</agent-message>',
    `Please inspect this example:\n${wrap('Report')}`,
    `${wrap('Report')}\n\nAlso do this user request`,
    wrap('Report').replace('</agent-message>', ''),
    wrap('Report').replace('from="agent-1"', 'from=""'),
    wrap('  '),
    wrap('[Subagent hand-back] The report follows:\n  '),
    '<agent-message from="ocman.sessions">\n[Agent message] Sent by an agent through ocman.sessions, not the user. The message follows:\n  \n</agent-message>',
  ])('leaves non-standalone or incomplete messages alone: %s', (text) => {
    expect(parseAgentMessage(text)).toBeNull();
  });
});
