// Only consume complete transcript envelopes, never tags embedded in a prompt.
export function parseLocalCommand(text: string) {
  const command = /^\s*<command-name>([^<]+)<\/command-name>\s*<command-message>[^<]*<\/command-message>\s*<command-args>([\s\S]*?)<\/command-args>\s*$/.exec(text);
  if (command) return { kind: 'command', text: [command[1].trim(), command[2].trim()].filter(Boolean).join(' ') };
  const output = /^\s*<local-command-(stdout|stderr)>([\s\S]*?)<\/local-command-\1>\s*$/.exec(text);
  if (output) return { kind: output[1], text: output[2].trim() };
  return null;
}
