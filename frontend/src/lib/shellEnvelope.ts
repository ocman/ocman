// Consume only a complete result, never a quoted example or a partial stream.
// V2 interpolates attributes verbatim, including quotes and ampersands in the
// command. The final attribute ends at the header's newline, not an XML quote.
export function parseShellEnvelope(text: unknown) {
  if (typeof text !== 'string') return null;
  const prefix = 'The following shell command was executed by the user:\n';
  const userExecuted = text.startsWith(prefix);
  const envelope = /^\s*<shell id="([^"\r\n]+)" state="([^"\r\n]+)" command="([\s\S]*?)">\r?\n([\s\S]*)\r?\n<\/shell>\s*$/.exec(userExecuted ? text.slice(prefix.length) : text);
  if (!envelope) return null;
  const [, id, state, command, output] = envelope;
  if (!command.trim() || !['running', 'pending', 'completed', 'error', 'cancelled'].includes(state)) return null;
  if (/<\/?shell(?:\s|>)/.test(output)) return null;
  return {
    id, command, userExecuted,
    status: state === 'cancelled' ? 'error' : state,
    output,
  };
}
