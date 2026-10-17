import type { PartData } from './api';

/** Only suppress platform-injected project rules, never a user's pasted file. */
export function isInjectedInstruction(part: PartData): boolean {
  return part.type === 'text' && part.synthetic === true
    && /^Instructions from: [^\r\n]+[/\\]AGENTS\.md\r?\n/.test(part.text ?? '');
}
