import { parseShellEnvelope } from './shellEnvelope';
import { parseSubagentEnvelope } from './subagentEnvelope';

export function parseV2Notification(text: string) {
  const shell = parseShellEnvelope(text);
  if (shell) return { kind: 'shell' as const, shell };
  const subagent = parseSubagentEnvelope(text);
  return subagent ? { kind: 'subagent' as const, subagent } : null;
}
