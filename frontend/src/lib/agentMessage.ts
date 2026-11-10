// Display-only parsing. The stored user-role envelope and its safety context
// remain unchanged; this does not authenticate the sender or grant authority.
export function parseAgentMessage(text: string) {
  const envelope = /^\s*(?:Another Claude session sent a message:\s*\n)?<agent-message from="([^"<>\r\n]+)">\r?\n([\s\S]*)\r?\n<\/agent-message>(?:\s*\n\s*\n(That "other Claude session" is an agent working inside this[\s\S]*))?\s*$/.exec(text);
  if (!envelope) return null;

  let result = envelope[2].trim();
  const mcpDelivery = envelope[1] === 'ocman.sessions';
  const details = [[mcpDelivery ? 'Source' : 'Agent ID', envelope[1]]];
  const mcpFrame = mcpDelivery && /^(\[Agent message\] Sent by an agent through ocman\.sessions, not the user\. The message follows:)\r?\n([\s\S]*)$/.exec(envelope[2]);
  if (mcpFrame) {
    details.push(['Delivery context', mcpFrame[1]]);
    result = mcpFrame[2].trim();
  }
  const handBack = /^(\[Subagent hand-back\][\s\S]*?The report follows:)\r?\n([\s\S]*)$/.exec(envelope[2].trimStart());
  if (handBack) {
    details.push(['Delivery context', handBack[1]]);
    // The delivery frame indents each report line. Remove only that common
    // prefix so Markdown headings work without flattening nested code/lists.
    result = handBack[2].replace(/^ {2}/gm, '').trim();
  }
  if (!result) return null;
  if (envelope[3]) details.push(['Delivery note', envelope[3].trim()]);
  return { summary: mcpDelivery ? 'Agent message' : 'Subagent feedback', status: '', result, details };
}
