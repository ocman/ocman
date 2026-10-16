import { describe, expect, it } from 'vitest';
import { parseShellEnvelope } from './shellEnvelope';

describe('parseShellEnvelope', () => {
  it('preserves raw quotes, entities, redirections and indented output', () => {
    expect(parseShellEnvelope('<shell id="sh_1" state="completed" command="echo "&amp;" > file 2>&1">\n&lt;img src=x&gt;\n  indented\n</shell>')).toEqual({
      id: 'sh_1', command: 'echo "&amp;" > file 2>&1', status: 'completed', userExecuted: false,
      output: '&lt;img src=x&gt;\n  indented',
    });
  });

  it('supports multiline commands, CRLF and empty output', () => {
    expect(parseShellEnvelope('<shell id="sh_1" state="completed" command="echo hi\necho bye">\r\n\r\n</shell>')?.command).toBe('echo hi\necho bye');
    expect(parseShellEnvelope('<shell id="sh_1" state="completed" command="true">\n\n</shell>')?.output).toBe('');
  });

  it('recognizes the exact user-executed preamble', () => {
    expect(parseShellEnvelope('The following shell command was executed by the user:\n<shell id="sh_1" state="completed" command="pwd">\n/workspace\n</shell>')?.userExecuted).toBe(true);
  });

  it.each(['running', 'pending', 'completed', 'error', 'cancelled'])('reads %s state', (state) => {
    expect(parseShellEnvelope(`<shell id="sh_1" state="${state}" command="true">\n\n</shell>`)?.status)
      .toBe(state === 'cancelled' ? 'error' : state);
  });

  it.each([
    null, {}, '',
    '<shell id="sh_1" state="completed" command="ls">\npartial',
    'Example: <shell id="sh_1" state="completed" command="ls">\noutput\n</shell>',
    '```xml\n<shell id="sh_1" state="completed" command="ls">\noutput\n</shell>\n```',
    '<shell state="completed" command="ls">\noutput\n</shell>',
    '<shell id="sh_1" command="ls">\noutput\n</shell>',
    '<shell id="sh_1" state="completed">\noutput\n</shell>',
    '<shell id="sh_1" state="completed" command=" ">\noutput\n</shell>',
    '<shell id="sh_1" state="unknown" command="ls">\noutput\n</shell>',
    '<shell id="sh_1" state="completed" command="ls">\n<shell>nested</shell>\n</shell>',
    '<shell id="sh_1" state="completed" command="ls">\none\n</shell><shell>two</shell>',
  ])('leaves malformed or embedded content unchanged: %s', (text) => {
    expect(parseShellEnvelope(text)).toBeNull();
  });
});
